// Package evaluator provides LLM-based evaluation of terminal pane content.
//
// This package is the LLM fallback path for agents not handled by
// deterministic parsers in the parser package. It constructs prompts from
// pane content and parses the LLM's structured verdict response.
package evaluator

import (
	"context"

	"github.com/timvw/pane-patrol/internal/model"
)

// Evaluator sends pane content to an LLM and returns a verdict.
type Evaluator interface {
	// Evaluate sends the pane content to an LLM and returns the verdict.
	Evaluate(ctx context.Context, content string) (*model.LLMVerdict, error)

	// Provider returns the provider name (e.g., "anthropic", "openai").
	Provider() string

	// Model returns the model name used for evaluation.
	Model() string
}
