// Package evaluator provides LLM-based evaluation of terminal pane content.
//
// ZFC compliance: This package is the bridge between transport (pane capture)
// and judgment (LLM). Go code constructs the prompt and parses the response,
// but ALL classification decisions (is it an agent? is it blocked? why?) are
// made by the LLM. Go never interprets pane content directly.
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
