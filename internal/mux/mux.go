// Package mux provides an abstraction over terminal multiplexers (tmux, zellij).
//
// ZFC compliance: This package is pure transport. It captures observable reality
// (pane content, session topology, process names) without interpreting or
// classifying any of it. All judgment calls are deferred to the LLM evaluator.
package mux

import (
	"context"

	"github.com/timvw/pane-patrol/internal/model"
)

// Multiplexer abstracts terminal multiplexer operations.
// Implementations exist for tmux and (future) zellij.
type Multiplexer interface {
	// Name returns the multiplexer name (e.g., "tmux", "zellij").
	Name() string

	// ListPanes returns all panes, optionally filtered by a session name regex pattern.
	// An empty filter returns all panes.
	ListPanes(ctx context.Context, filter string) ([]model.Pane, error)

	// CapturePane captures the visible content of a pane.
	// The target format depends on the multiplexer (e.g., "session:window.pane" for tmux).
	CapturePane(ctx context.Context, target string) (string, error)
}
