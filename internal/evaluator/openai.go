package evaluator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/timvw/pane-patrol/internal/model"
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

	resp, err := e.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: e.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(SystemPrompt),
			openai.UserMessage(userMessage),
		},
		MaxCompletionTokens: openai.Int(e.maxTokens),
	})
	if err != nil {
		return nil, fmt.Errorf("openai API call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai API returned empty response")
	}

	text := stripMarkdownFences(resp.Choices[0].Message.Content)

	var verdict model.LLMVerdict
	if err := json.Unmarshal([]byte(text), &verdict); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response as JSON: %w\nraw response: %s", err, text)
	}

	return &verdict, nil
}
