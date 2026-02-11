package evaluator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/timvw/pane-patrol/internal/model"
)

// AnthropicEvaluator evaluates pane content using the Anthropic Messages API.
// Works with both direct Anthropic API and Azure AI Foundry.
type AnthropicEvaluator struct {
	client anthropic.Client
	model  string
}

// AnthropicConfig holds configuration for the Anthropic evaluator.
type AnthropicConfig struct {
	// BaseURL is the API endpoint (e.g., "https://resource.services.ai.azure.com/anthropic/v1").
	BaseURL string
	// APIKey is the API key.
	APIKey string
	// Model is the model name (e.g., "claude-haiku-4-5").
	Model string
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

	return &AnthropicEvaluator{
		client: client,
		model:  cfg.Model,
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

// Evaluate sends pane content to the Anthropic API and returns the verdict.
func (e *AnthropicEvaluator) Evaluate(ctx context.Context, content string) (*model.LLMVerdict, error) {
	userMessage := UserPromptTemplate + content

	resp, err := e.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(e.model),
		MaxTokens: 512,
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
		return nil, fmt.Errorf("anthropic API call failed: %w", err)
	}

	if len(resp.Content) == 0 {
		return nil, fmt.Errorf("anthropic API returned empty response")
	}

	// Extract text from the first content block.
	text := stripMarkdownFences(resp.Content[0].Text)

	var verdict model.LLMVerdict
	if err := json.Unmarshal([]byte(text), &verdict); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response as JSON: %w\nraw response: %s", err, text)
	}

	return &verdict, nil
}
