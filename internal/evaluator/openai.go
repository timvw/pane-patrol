package evaluator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/timvw/pane-patrol/internal/model"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// OpenAIEvaluator evaluates pane content using an OpenAI-compatible Chat Completions API.
// Works with OpenAI, Azure OpenAI, and any OpenAI-compatible endpoint.
type OpenAIEvaluator struct {
	client    openai.Client
	model     string
	maxTokens int64
}

// OpenAIConfig holds configuration for the OpenAI evaluator.
type OpenAIConfig struct {
	// BaseURL is the API endpoint.
	BaseURL string
	// APIKey is the API key.
	APIKey string
	// Model is the model name (e.g., "gpt-4o-mini").
	Model string
	// MaxTokens is the maximum number of completion tokens.
	// For reasoning models (gpt-5, gpt-5.1), this must be large enough
	// to accommodate both reasoning tokens and output content.
	MaxTokens int64
	// ExtraHeaders are additional HTTP headers.
	ExtraHeaders map[string]string
}

// NewOpenAIEvaluator creates a new OpenAI-compatible evaluator.
func NewOpenAIEvaluator(cfg OpenAIConfig) *OpenAIEvaluator {
	var opts []option.RequestOption

	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	for k, v := range cfg.ExtraHeaders {
		opts = append(opts, option.WithHeader(k, v))
	}

	client := openai.NewClient(opts...)

	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	return &OpenAIEvaluator{
		client:    client,
		model:     cfg.Model,
		maxTokens: maxTokens,
	}
}

// Provider returns "openai".
func (e *OpenAIEvaluator) Provider() string {
	return "openai"
}

// Model returns the model name.
func (e *OpenAIEvaluator) Model() string {
	return e.model
}

// Evaluate sends pane content to an OpenAI-compatible API and returns the verdict.
func (e *OpenAIEvaluator) Evaluate(ctx context.Context, content string) (*model.LLMVerdict, error) {
	userMessage := UserPromptTemplate + content

	// Start a GenAI generation span following OTel GenAI semantic conventions.
	// Span name: "{operation} {model}" per the spec.
	ctx, span := evalTracer.Start(ctx, "chat "+e.model,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			// GenAI semantic conventions (required + recommended)
			attribute.String("gen_ai.operation.name", "chat"),
			attribute.String("gen_ai.provider.name", "openai"),
			attribute.String("gen_ai.request.model", e.model),
			attribute.Int64("gen_ai.request.max_tokens", e.maxTokens),

			// Langfuse-specific: ensure this shows as a "generation"
			attribute.String("langfuse.observation.type", "generation"),
		),
	)
	defer span.End()

	// Record input (system + user messages as JSON)
	inputMessages := []map[string]string{
		{"role": "system", "content": SystemPrompt},
		{"role": "user", "content": userMessage},
	}
	if inputJSON, err := json.Marshal(inputMessages); err == nil {
		span.SetAttributes(attribute.String("gen_ai.input.messages", string(inputJSON)))
	}

	resp, err := e.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: e.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(SystemPrompt),
			openai.UserMessage(userMessage),
		},
		MaxCompletionTokens: openai.Int(e.maxTokens),
	})
	if err != nil {
		span.SetAttributes(attribute.String("error.type", "api_error"))
		return nil, fmt.Errorf("openai API call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		span.SetAttributes(attribute.String("error.type", "empty_response"))
		return nil, fmt.Errorf("openai API returned empty response")
	}

	rawText := resp.Choices[0].Message.Content
	text := stripMarkdownFences(rawText)

	// Record response attributes
	span.SetAttributes(
		attribute.String("gen_ai.response.model", resp.Model),
		attribute.String("gen_ai.response.id", resp.ID),
		attribute.Int64("gen_ai.usage.input_tokens", resp.Usage.PromptTokens),
		attribute.Int64("gen_ai.usage.output_tokens", resp.Usage.CompletionTokens),
	)
	if resp.Choices[0].FinishReason != "" {
		span.SetAttributes(attribute.StringSlice("gen_ai.response.finish_reasons", []string{string(resp.Choices[0].FinishReason)}))
	}

	// Record output
	outputMessages := []map[string]string{
		{"role": "assistant", "content": rawText},
	}
	if outputJSON, err := json.Marshal(outputMessages); err == nil {
		span.SetAttributes(attribute.String("gen_ai.output.messages", string(outputJSON)))
	}

	var verdict model.LLMVerdict
	if err := json.Unmarshal([]byte(text), &verdict); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response as JSON: %w\nraw response: %s", err, text)
	}

	// Capture token usage from response
	verdict.Usage = model.TokenUsage{
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
	}

	return &verdict, nil
}
