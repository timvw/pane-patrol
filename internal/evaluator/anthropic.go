package evaluator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/timvw/pane-patrol/internal/model"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// AnthropicEvaluator evaluates pane content using the Anthropic Messages API.
// Works with both direct Anthropic API and Azure AI Foundry.
type AnthropicEvaluator struct {
	client    anthropic.Client
	model     string
	maxTokens int64
}

// AnthropicConfig holds configuration for the Anthropic evaluator.
type AnthropicConfig struct {
	// BaseURL is the API endpoint (e.g., "https://resource.services.ai.azure.com/anthropic/v1").
	BaseURL string
	// APIKey is the API key.
	APIKey string
	// Model is the model name (e.g., "claude-haiku-4-5").
	Model string
	// MaxTokens is the maximum number of output tokens.
	MaxTokens int64
	// ExtraHeaders are additional HTTP headers (e.g., "api-key" for Azure).
	ExtraHeaders map[string]string
}

// NewAnthropicEvaluator creates a new Anthropic evaluator.
func NewAnthropicEvaluator(cfg AnthropicConfig) *AnthropicEvaluator {
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

	client := anthropic.NewClient(opts...)

	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	return &AnthropicEvaluator{
		client:    client,
		model:     cfg.Model,
		maxTokens: maxTokens,
	}
}

// Provider returns "anthropic".
func (e *AnthropicEvaluator) Provider() string {
	return "anthropic"
}

// Model returns the model name.
func (e *AnthropicEvaluator) Model() string {
	return e.model
}

var evalTracer = otel.Tracer("pane-patrol/evaluator")

// Evaluate sends pane content to the Anthropic API and returns the verdict.
func (e *AnthropicEvaluator) Evaluate(ctx context.Context, content string) (*model.LLMVerdict, error) {
	userMessage := UserPromptTemplate + content

	// Start a GenAI generation span following OTel GenAI semantic conventions.
	// Span name: "{operation} {model}" per the spec.
	ctx, span := evalTracer.Start(ctx, "chat "+e.model,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			// GenAI semantic conventions (required + recommended)
			attribute.String("gen_ai.operation.name", "chat"),
			attribute.String("gen_ai.provider.name", "anthropic"),
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

	resp, err := e.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(e.model),
		MaxTokens: e.maxTokens,
		System: []anthropic.TextBlockParam{
			{Text: SystemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewTextBlock(userMessage),
			),
		},
	})
	if err != nil {
		span.SetAttributes(attribute.String("error.type", "api_error"))
		return nil, fmt.Errorf("anthropic API call failed: %w", err)
	}

	if len(resp.Content) == 0 {
		span.SetAttributes(attribute.String("error.type", "empty_response"))
		return nil, fmt.Errorf("anthropic API returned empty response")
	}

	// Extract text from the first content block.
	rawText := resp.Content[0].Text
	text := stripMarkdownFences(rawText)

	// Record response attributes
	span.SetAttributes(
		attribute.String("gen_ai.response.model", e.model),
		attribute.Int64("gen_ai.usage.input_tokens", resp.Usage.InputTokens),
		attribute.Int64("gen_ai.usage.output_tokens", resp.Usage.OutputTokens),
	)
	if string(resp.StopReason) != "" {
		span.SetAttributes(attribute.StringSlice("gen_ai.response.finish_reasons", []string{string(resp.StopReason)}))
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
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
	}

	return &verdict, nil
}
