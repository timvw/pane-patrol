package supervisor

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/timvw/pane-patrol/internal/model"
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

// mockEvaluator implements evaluator.Evaluator for testing.
type mockEvaluator struct {
	verdicts map[string]*model.LLMVerdict // content -> verdict
	evalErr  error
	calls    int64 // accessed atomically — scanner runs goroutines in parallel
}

func (e *mockEvaluator) Provider() string { return "mock" }
func (e *mockEvaluator) Model() string    { return "mock-model" }

func (e *mockEvaluator) Evaluate(_ context.Context, content string) (*model.LLMVerdict, error) {
	atomic.AddInt64(&e.calls, 1)
	if e.evalErr != nil {
		return nil, e.evalErr
	}
	if v, ok := e.verdicts[content]; ok {
		return v, nil
	}
	// Default: not an agent
	return &model.LLMVerdict{
		Agent:   "not_an_agent",
		Blocked: false,
		Reason:  "default mock verdict",
		Usage:   model.TokenUsage{InputTokens: 100, OutputTokens: 50},
	}, nil
}

func (e *mockEvaluator) callCount() int64 {
	return atomic.LoadInt64(&e.calls)
}

func TestScanner_BasicScan(t *testing.T) {
	mux := &mockMultiplexer{
		panes: []model.Pane{
			{Target: "dev:0.0", Session: "dev", Window: 0, Pane: 0, PID: 1234, Command: "bash"},
			{Target: "dev:0.1", Session: "dev", Window: 0, Pane: 1, PID: 5678, Command: "zsh"},
		},
		captures: map[string]string{
			"dev:0.0": "$ opencode\nThinking...",
			"dev:0.1": "$ ls\nfoo bar",
		},
	}

	eval := &mockEvaluator{verdicts: map[string]*model.LLMVerdict{}}

	scanner := &Scanner{
		Mux:       mux,
		Evaluator: eval,
		Parallel:  2,
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if len(result.Verdicts) != 2 {
		t.Fatalf("got %d verdicts, want 2", len(result.Verdicts))
	}

	// Both should have been evaluated
	if eval.callCount() != 2 {
		t.Errorf("evaluator called %d times, want 2", eval.callCount())
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

	eval := &mockEvaluator{verdicts: map[string]*model.LLMVerdict{}}

	scanner := &Scanner{
		Mux:             mux,
		Evaluator:       eval,
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

	eval := &mockEvaluator{verdicts: map[string]*model.LLMVerdict{}}

	scanner := &Scanner{
		Mux:        mux,
		Evaluator:  eval,
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

	eval := &mockEvaluator{verdicts: map[string]*model.LLMVerdict{}}

	scanner := &Scanner{
		Mux:       mux,
		Evaluator: eval,
		Parallel:  5,
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if len(result.Verdicts) != 0 {
		t.Errorf("got %d verdicts, want 0", len(result.Verdicts))
	}
	if eval.callCount() != 0 {
		t.Errorf("evaluator called %d times, want 0", eval.callCount())
	}
}

func TestScanner_EvaluationError(t *testing.T) {
	mux := &mockMultiplexer{
		panes: []model.Pane{
			{Target: "dev:0.0", Session: "dev", PID: 1, Command: "bash"},
		},
		captures: map[string]string{
			"dev:0.0": "content",
		},
	}

	eval := &mockEvaluator{
		evalErr: fmt.Errorf("LLM unavailable"),
	}

	scanner := &Scanner{
		Mux:       mux,
		Evaluator: eval,
		Parallel:  1,
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
	mux := &mockMultiplexer{
		panes: []model.Pane{
			{Target: "dev:0.0", Session: "dev", PID: 1, Command: "bash"},
		},
		captures: map[string]string{
			"dev:0.0": "same content",
		},
	}

	eval := &mockEvaluator{verdicts: map[string]*model.LLMVerdict{}}
	cache := NewVerdictCache(5 * time.Minute)

	scanner := &Scanner{
		Mux:       mux,
		Evaluator: eval,
		Parallel:  1,
		Cache:     cache,
	}

	// First scan — cache miss, LLM called
	result1, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan 1 error: %v", err)
	}
	if eval.callCount() != 1 {
		t.Errorf("Scan 1: evaluator called %d times, want 1", eval.callCount())
	}
	if result1.CacheHits != 0 {
		t.Errorf("Scan 1: got %d cache hits, want 0", result1.CacheHits)
	}

	// Second scan — same content, should be cache hit
	result2, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan 2 error: %v", err)
	}
	if eval.callCount() != 1 {
		t.Errorf("Scan 2: evaluator called %d times, want 1 (cache should prevent 2nd call)", eval.callCount())
	}
	if result2.CacheHits != 1 {
		t.Errorf("Scan 2: got %d cache hits, want 1", result2.CacheHits)
	}
}

func TestScanner_CacheInvalidatedOnContentChange(t *testing.T) {
	captures := map[string]string{
		"dev:0.0": "original content",
	}

	mux := &mockMultiplexer{
		panes: []model.Pane{
			{Target: "dev:0.0", Session: "dev", PID: 1, Command: "bash"},
		},
		captures: captures,
	}

	eval := &mockEvaluator{verdicts: map[string]*model.LLMVerdict{}}
	cache := NewVerdictCache(5 * time.Minute)

	scanner := &Scanner{
		Mux:       mux,
		Evaluator: eval,
		Parallel:  1,
		Cache:     cache,
	}

	// First scan
	_, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan 1 error: %v", err)
	}
	if eval.callCount() != 1 {
		t.Fatalf("Scan 1: want 1 eval call, got %d", eval.callCount())
	}

	// Change pane content
	captures["dev:0.0"] = "new content after agent progressed"

	// Second scan — content changed, should miss cache
	_, err = scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan 2 error: %v", err)
	}
	if eval.callCount() != 2 {
		t.Errorf("Scan 2: want 2 eval calls (cache miss on changed content), got %d", eval.callCount())
	}
}

func TestScanner_ProcessHeaderIncluded(t *testing.T) {
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
			"dev:0.0": "$ opencode\nThinking...",
		},
	}

	// Track what content the evaluator receives
	var receivedContent string
	eval := &mockEvaluator{verdicts: map[string]*model.LLMVerdict{}}
	origEval := eval

	// Use a wrapper to capture the content
	wrapper := &contentCapturingEvaluator{inner: origEval, captured: &receivedContent}

	scanner := &Scanner{
		Mux:       mux,
		Evaluator: wrapper,
		Parallel:  1,
	}

	_, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	// The content sent to the evaluator should include process header
	if receivedContent == "" {
		t.Fatal("evaluator received empty content")
	}
	if !contains(receivedContent, "[Process Info]") {
		t.Error("content missing [Process Info] header")
	}
	if !contains(receivedContent, "opencode --model gpt-4o") {
		t.Error("content missing process tree entry")
	}
	if !contains(receivedContent, "[Terminal Content]") {
		t.Error("content missing [Terminal Content] header")
	}
}

// contentCapturingEvaluator wraps an evaluator and captures the content.
type contentCapturingEvaluator struct {
	inner    *mockEvaluator
	captured *string
}

func (e *contentCapturingEvaluator) Provider() string { return e.inner.Provider() }
func (e *contentCapturingEvaluator) Model() string    { return e.inner.Model() }
func (e *contentCapturingEvaluator) Evaluate(ctx context.Context, content string) (*model.LLMVerdict, error) {
	*e.captured = content
	return e.inner.Evaluate(ctx, content)
}

func TestScanner_ListPanesError(t *testing.T) {
	mux := &mockMultiplexer{
		listErr: fmt.Errorf("tmux not running"),
	}

	eval := &mockEvaluator{verdicts: map[string]*model.LLMVerdict{}}

	scanner := &Scanner{
		Mux:       mux,
		Evaluator: eval,
		Parallel:  1,
	}

	_, err := scanner.Scan(context.Background())
	if err == nil {
		t.Fatal("expected error when ListPanes fails")
	}
	if !contains(err.Error(), "failed to list panes") {
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

	eval := &mockEvaluator{verdicts: map[string]*model.LLMVerdict{}}

	scanner := &Scanner{
		Mux:       mux,
		Evaluator: eval,
		Parallel:  1,
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

func TestScanner_WaitingForWiredFromLLM(t *testing.T) {
	mux := &mockMultiplexer{
		panes: []model.Pane{
			{Target: "dev:0.0", Session: "dev", Window: 0, Pane: 0, PID: 1234, Command: "bash"},
		},
		captures: map[string]string{
			"dev:0.0": "some unknown agent\nDo you want to proceed?\n1. Yes  2. No",
		},
	}

	// Build the expected content key (process header + capture)
	pane := mux.panes[0]
	expectedContent := model.BuildProcessHeader(pane) + mux.captures["dev:0.0"]

	eval := &mockEvaluator{
		verdicts: map[string]*model.LLMVerdict{
			expectedContent: {
				Agent:      "unknown_agent",
				Blocked:    true,
				Reason:     "permission dialog",
				WaitingFor: "Do you want to proceed?\n1. Yes  2. No",
				Actions: []model.Action{
					{Keys: "y", Label: "approve", Risk: "medium"},
				},
				Recommended: 0,
				Reasoning:   "saw a permission dialog",
				Usage:       model.TokenUsage{InputTokens: 200, OutputTokens: 100},
			},
		},
	}

	scanner := &Scanner{
		Mux:       mux,
		Evaluator: eval,
		Parallel:  1,
		// No Parsers — force LLM fallback
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if len(result.Verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(result.Verdicts))
	}

	v := result.Verdicts[0]
	if v.WaitingFor != "Do you want to proceed?\n1. Yes  2. No" {
		t.Errorf("WaitingFor: got %q, want %q", v.WaitingFor, "Do you want to proceed?\n1. Yes  2. No")
	}
	if v.Recommended != 0 {
		t.Errorf("Recommended: got %d, want 0", v.Recommended)
	}
	if len(v.Actions) != 1 {
		t.Errorf("Actions: got %d, want 1", len(v.Actions))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsSubstr(s, substr)
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
