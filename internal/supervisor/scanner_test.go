package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/timvw/pane-patrol/internal/model"
	"github.com/timvw/pane-patrol/internal/parser"
)

// mockMultiplexer implements mux.Multiplexer for testing.
type mockMultiplexer struct {
	panes    []model.Pane
	captures map[string]string // target -> content
	listErr  error
	captErr  error
}

func (m *mockMultiplexer) Name() string { return "mock" }

func (m *mockMultiplexer) ListPanes(_ context.Context, filter string) ([]model.Pane, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.panes, nil
}

func (m *mockMultiplexer) CapturePane(_ context.Context, target string) (string, error) {
	if m.captErr != nil {
		return "", m.captErr
	}
	content, ok := m.captures[target]
	if !ok {
		return "", fmt.Errorf("no capture for %q", target)
	}
	return content, nil
}

func TestScanner_BasicScan(t *testing.T) {
	mux := &mockMultiplexer{
		panes: []model.Pane{
			{Target: "dev:0.0", Session: "dev", Window: 0, Pane: 0, PID: 1234, Command: "bash"},
			{Target: "dev:0.1", Session: "dev", Window: 0, Pane: 1, PID: 5678, Command: "zsh"},
		},
		captures: map[string]string{
			"dev:0.0": "$ ls\nfoo bar",
			"dev:0.1": "$ ls\nfoo bar",
		},
	}

	scanner := &Scanner{
		Mux:      mux,
		Parsers:  parser.NewRegistry(),
		Parallel: 2,
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if len(result.Verdicts) != 2 {
		t.Fatalf("got %d verdicts, want 2", len(result.Verdicts))
	}

	// Both panes have no matching parser, so they should be "unknown"
	for i, v := range result.Verdicts {
		if v.Agent != "unknown" {
			t.Errorf("verdict[%d].Agent: got %q, want %q", i, v.Agent, "unknown")
		}
	}
}

func TestScanner_ExcludeSessions(t *testing.T) {
	mux := &mockMultiplexer{
		panes: []model.Pane{
			{Target: "dev:0.0", Session: "dev", PID: 1, Command: "bash"},
			{Target: "AIGGTM-123:0.0", Session: "AIGGTM-123", PID: 2, Command: "bash"},
			{Target: "private:0.0", Session: "private", PID: 3, Command: "bash"},
		},
		captures: map[string]string{
			"dev:0.0":        "content",
			"AIGGTM-123:0.0": "content",
			"private:0.0":    "content",
		},
	}

	scanner := &Scanner{
		Mux:             mux,
		Parsers:         parser.NewRegistry(),
		ExcludeSessions: []string{"AIGGTM-*", "private"},
		Parallel:        5,
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if len(result.Verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1 (only dev)", len(result.Verdicts))
	}
	if result.Verdicts[0].Target != "dev:0.0" {
		t.Errorf("expected dev:0.0, got %q", result.Verdicts[0].Target)
	}
}

func TestScanner_SelfExclusion(t *testing.T) {
	mux := &mockMultiplexer{
		panes: []model.Pane{
			{Target: "dev:0.0", Session: "dev", PID: 1, Command: "bash"},
			{Target: "supervisor:0.0", Session: "supervisor", PID: 2, Command: "pane-patrol"},
		},
		captures: map[string]string{
			"dev:0.0":        "content",
			"supervisor:0.0": "content",
		},
	}

	scanner := &Scanner{
		Mux:        mux,
		Parsers:    parser.NewRegistry(),
		SelfTarget: "supervisor:0.0",
		Parallel:   5,
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if len(result.Verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1 (self excluded)", len(result.Verdicts))
	}
	if result.Verdicts[0].Target != "dev:0.0" {
		t.Errorf("expected dev:0.0, got %q", result.Verdicts[0].Target)
	}
}

func TestScanner_EmptyPanes(t *testing.T) {
	mux := &mockMultiplexer{
		panes:    []model.Pane{},
		captures: map[string]string{},
	}

	scanner := &Scanner{
		Mux:      mux,
		Parsers:  parser.NewRegistry(),
		Parallel: 5,
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if len(result.Verdicts) != 0 {
		t.Errorf("got %d verdicts, want 0", len(result.Verdicts))
	}
}

func TestScanner_EvaluationError(t *testing.T) {
	// Test that capture failures are handled gracefully
	mux := &mockMultiplexer{
		panes: []model.Pane{
			{Target: "dev:0.0", Session: "dev", PID: 1, Command: "bash"},
		},
		captures: map[string]string{
			"dev:0.0": "content",
		},
		captErr: fmt.Errorf("pane no longer exists"),
	}

	scanner := &Scanner{
		Mux:      mux,
		Parsers:  parser.NewRegistry(),
		Parallel: 1,
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() should not return error for per-pane failures: %v", err)
	}

	if len(result.Verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(result.Verdicts))
	}

	v := result.Verdicts[0]
	if v.Agent != "error" {
		t.Errorf("Agent: got %q, want %q", v.Agent, "error")
	}
	if v.Blocked {
		t.Error("Blocked: got true, want false for error verdicts")
	}
}

func TestScanner_CacheHit(t *testing.T) {
	// OpenCode idle prompt content that the parser recognizes
	openCodeContent := "\n\n\n\n\n\n\n\n\n\n> "

	mux := &mockMultiplexer{
		panes: []model.Pane{
			{Target: "dev:0.0", Session: "dev", PID: 1, Command: "bash", ProcessTree: []string{"opencode"}},
		},
		captures: map[string]string{
			"dev:0.0": openCodeContent,
		},
	}

	cache := NewVerdictCache(5 * time.Minute)

	scanner := &Scanner{
		Mux:      mux,
		Parsers:  parser.NewRegistry(),
		Parallel: 1,
		Cache:    cache,
	}

	// First scan — cache miss, parser produces result
	result1, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan 1 error: %v", err)
	}
	if result1.CacheHits != 0 {
		t.Errorf("Scan 1: got %d cache hits, want 0", result1.CacheHits)
	}
	if len(result1.Verdicts) != 1 {
		t.Fatalf("Scan 1: got %d verdicts, want 1", len(result1.Verdicts))
	}
	if result1.Verdicts[0].EvalSource != model.EvalSourceParser {
		t.Errorf("Scan 1: got eval_source %q, want %q", result1.Verdicts[0].EvalSource, model.EvalSourceParser)
	}

	// Second scan — same content, should be cache hit
	result2, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan 2 error: %v", err)
	}
	if result2.CacheHits != 1 {
		t.Errorf("Scan 2: got %d cache hits, want 1", result2.CacheHits)
	}
}

func TestScanner_CacheInvalidatedOnContentChange(t *testing.T) {
	// OpenCode idle prompt content that the parser recognizes
	openCodeContent1 := "\n\n\n\n\n\n\n\n\n\n> "
	openCodeContent2 := "\n\n\n\n\n\n\n\nDone.\n\n> "

	captures := map[string]string{
		"dev:0.0": openCodeContent1,
	}

	mux := &mockMultiplexer{
		panes: []model.Pane{
			{Target: "dev:0.0", Session: "dev", PID: 1, Command: "bash", ProcessTree: []string{"opencode"}},
		},
		captures: captures,
	}

	cache := NewVerdictCache(5 * time.Minute)

	scanner := &Scanner{
		Mux:      mux,
		Parsers:  parser.NewRegistry(),
		Parallel: 1,
		Cache:    cache,
	}

	// First scan
	result1, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan 1 error: %v", err)
	}
	if result1.CacheHits != 0 {
		t.Errorf("Scan 1: got %d cache hits, want 0", result1.CacheHits)
	}

	// Change pane content
	captures["dev:0.0"] = openCodeContent2

	// Second scan — content changed, should miss cache
	result2, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan 2 error: %v", err)
	}
	if result2.CacheHits != 0 {
		t.Errorf("Scan 2: got %d cache hits, want 0 (content changed)", result2.CacheHits)
	}
}

func TestScanner_ProcessHeaderIncluded(t *testing.T) {
	// Use content that the OpenCode parser will recognize so we get a parser result
	openCodeContent := "\n\n\n\n\n\n\n\n\n\n> "

	mux := &mockMultiplexer{
		panes: []model.Pane{
			{
				Target:      "dev:0.0",
				Session:     "dev",
				PID:         12345,
				Command:     "bash",
				ProcessTree: []string{"opencode --model gpt-4o"},
			},
		},
		captures: map[string]string{
			"dev:0.0": openCodeContent,
		},
	}

	scanner := &Scanner{
		Mux:      mux,
		Parsers:  parser.NewRegistry(),
		Parallel: 1,
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if len(result.Verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(result.Verdicts))
	}

	// The parser should have matched (OpenCode idle prompt)
	v := result.Verdicts[0]
	if v.Agent != "opencode" {
		t.Errorf("Agent: got %q, want %q", v.Agent, "opencode")
	}
	if v.EvalSource != model.EvalSourceParser {
		t.Errorf("EvalSource: got %q, want %q", v.EvalSource, model.EvalSourceParser)
	}
}

func TestScanner_ListPanesError(t *testing.T) {
	mux := &mockMultiplexer{
		listErr: fmt.Errorf("tmux not running"),
	}

	scanner := &Scanner{
		Mux:      mux,
		Parsers:  parser.NewRegistry(),
		Parallel: 1,
	}

	_, err := scanner.Scan(context.Background())
	if err == nil {
		t.Fatal("expected error when ListPanes fails")
	}
	if !strings.Contains(err.Error(), "failed to list panes") {
		t.Errorf("error should wrap ListPanes failure, got: %v", err)
	}
}

func TestScanner_CapturePaneError(t *testing.T) {
	mux := &mockMultiplexer{
		panes: []model.Pane{
			{Target: "dev:0.0", Session: "dev", PID: 1, Command: "bash"},
		},
		captErr: fmt.Errorf("pane no longer exists"),
	}

	scanner := &Scanner{
		Mux:      mux,
		Parsers:  parser.NewRegistry(),
		Parallel: 1,
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() should not fail for per-pane capture errors: %v", err)
	}

	if len(result.Verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(result.Verdicts))
	}

	v := result.Verdicts[0]
	if v.Agent != "error" {
		t.Errorf("Agent: got %q, want %q", v.Agent, "error")
	}
}
