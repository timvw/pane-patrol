# Subagent Detection Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Detect OpenCode subagent processes in the process tree so idle-looking parent panes are correctly classified as "working (subagent active)" instead of "idle at prompt".

**Architecture:** The OpenCode parser already receives `processTree []string` on every `Parse()` call. We add a `findSubagents()` function that scans these entries for `opencode` + `-s` + `ses_` patterns. When subagents are found and no TUI active indicators are present, the parser returns a working verdict with subagent metadata.

**Tech Stack:** Go, standard library only (strings, regexp for session ID extraction)

---

### Task 1: Add SubagentInfo to model types

**Files:**
- Modify: `internal/model/types.go:78` (after Action struct)
- Modify: `internal/model/types.go:35-78` (Verdict struct)

**Step 1: Add SubagentInfo struct and Verdict field**

Add after the `Action` struct (line 92):

```go
// SubagentInfo describes a detected subagent child process.
type SubagentInfo struct {
	// SessionID is the subagent session identifier (e.g., "ses_abc123").
	SessionID string `json:"session_id"`
}
```

Add to the `Verdict` struct, after the `Recommended` field (line 65):

```go
	// Subagents lists detected subagent child processes.
	// Populated by deterministic parsers when a parent agent has active subagents.
	Subagents []SubagentInfo `json:"subagents,omitempty"`
```

**Step 2: Run tests to verify no breakage**

Run: `go test ./internal/model/...`
Expected: PASS (struct addition is backward compatible)

**Step 3: Commit**

```bash
git add internal/model/types.go
git commit -m "feat: add SubagentInfo struct and Subagents field to Verdict"
```

---

### Task 2: Add Subagents field to parser Result

**Files:**
- Modify: `internal/parser/parser.go:20-28` (Result struct)

**Step 1: Add Subagents to Result**

Add to the `Result` struct after `Reasoning`:

```go
	Subagents []model.SubagentInfo
```

**Step 2: Run tests to verify no breakage**

Run: `go test ./internal/parser/...`
Expected: PASS (struct addition is backward compatible)

**Step 3: Commit**

```bash
git add internal/parser/parser.go
git commit -m "feat: add Subagents field to parser Result"
```

---

### Task 3: Wire Subagents from parser Result to Verdict

**Files:**
- Modify: `internal/supervisor/scanner.go:202-209` (Result-to-Verdict mapping)
- Modify: `cmd/scan.go:132-141` (Result-to-Verdict mapping)

**Step 1: Add `v.Subagents = parsed.Subagents` to both mapping sites**

In `internal/supervisor/scanner.go`, after line 209 (`v.Recommended = parsed.Recommended`):

```go
			v.Subagents = parsed.Subagents
```

In `cmd/scan.go`, after line 140 (`v.Recommended = parsed.Recommended`):

```go
		v.Subagents = parsed.Subagents
```

**Step 2: Run tests**

Run: `go test ./internal/supervisor/... ./cmd/...`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/supervisor/scanner.go cmd/scan.go
git commit -m "feat: wire Subagents from parser Result to Verdict"
```

---

### Task 4: Write failing tests for subagent detection

**Files:**
- Modify: `internal/parser/parser_test.go` (add new test functions at end)

**Step 1: Write three test cases**

Add at the end of `parser_test.go`:

```go
// --- OpenCode Subagent Detection Tests ---

func TestOpenCode_SubagentInProcessTree_NotIdle(t *testing.T) {
	// Parent TUI shows idle prompt, but process tree has a subagent child.
	// Should be classified as working (subagent active), NOT idle.
	content := `
  some previous output...

  > 
`
	processTree := []string{
		"opencode --provider anthropic",
		"  opencode -s ses_abc123def456",
	}
	p := &OpenCodeParser{}
	result := p.Parse(content, processTree)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Agent != "opencode" {
		t.Errorf("agent: got %q, want %q", result.Agent, "opencode")
	}
	if result.Blocked {
		t.Error("expected blocked=false when subagent is active")
	}
	if !strings.Contains(result.Reason, "subagent") {
		t.Errorf("reason should mention subagent, got %q", result.Reason)
	}
	if len(result.Subagents) == 0 {
		t.Fatal("expected at least one subagent in result")
	}
	if result.Subagents[0].SessionID != "ses_abc123def456" {
		t.Errorf("session_id: got %q, want %q", result.Subagents[0].SessionID, "ses_abc123def456")
	}
}

func TestOpenCode_NoSubagent_StillIdle(t *testing.T) {
	// Parent TUI shows idle prompt, process tree has no subagent.
	// Should remain idle at prompt (unchanged behavior).
	content := `
  some previous output...

  > 
`
	processTree := []string{
		"opencode --provider anthropic",
	}
	p := &OpenCodeParser{}
	result := p.Parse(content, processTree)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true when no subagent and idle prompt")
	}
	if result.Reason != "idle at prompt" {
		t.Errorf("reason: got %q, want %q", result.Reason, "idle at prompt")
	}
}

func TestOpenCode_ActiveTUI_WithSubagent_TUIPrecedence(t *testing.T) {
	// Parent TUI shows active execution indicators AND has subagent.
	// TUI indicators take precedence (actively executing, no subagent info needed).
	content := `
  ▣ Build · claude-sonnet-4-5 · 12s

  ■■■⬝⬝⬝⬝⬝

  esc interrupt
`
	processTree := []string{
		"opencode --provider anthropic",
		"  opencode -s ses_xyz789",
	}
	p := &OpenCodeParser{}
	result := p.Parse(content, processTree)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false for active execution")
	}
	if result.Reason != "actively executing" {
		t.Errorf("reason: got %q, want %q", result.Reason, "actively executing")
	}
}

func TestOpenCode_SubagentDefaultFallthrough(t *testing.T) {
	// OpenCode TUI detected but no idle prompt and no active indicators.
	// Process tree has subagent. Should still detect as working via subagent.
	content := `
  some output from the agent
  more output lines here
  Task dispatched
`
	processTree := []string{
		"opencode --provider anthropic",
		"  opencode -s ses_task42session",
	}
	p := &OpenCodeParser{}
	result := p.Parse(content, processTree)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false when subagent is active")
	}
	if !strings.Contains(result.Reason, "subagent") {
		t.Errorf("reason should mention subagent, got %q", result.Reason)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/parser/... -run "TestOpenCode_Subagent|TestOpenCode_NoSubagent|TestOpenCode_ActiveTUI_WithSubagent"  -v`
Expected: FAIL — `TestOpenCode_SubagentInProcessTree_NotIdle` and `TestOpenCode_SubagentDefaultFallthrough` should fail (blocked=true instead of false). `TestOpenCode_NoSubagent_StillIdle` and `TestOpenCode_ActiveTUI_WithSubagent_TUIPrecedence` should pass (existing behavior).

**Step 3: Commit failing tests**

```bash
git add internal/parser/parser_test.go
git commit -m "test: add failing tests for subagent detection in OpenCode parser"
```

---

### Task 5: Implement findSubagents and wire into Parse()

**Files:**
- Modify: `internal/parser/opencode.go`

**Step 1: Add findSubagents function**

Add at the end of `opencode.go`, before the `extractBlock` function:

```go
// findSubagents scans the process tree for OpenCode subagent processes.
//
// OpenCode's Task tool dispatches subagents as child processes with the
// pattern: opencode -s ses_<session_id>
//
// Source: packages/opencode/src/cli/cmd/root.ts (the -s / --session flag)
//
// Each process tree entry is an indented command-line string from ps.
// Returns nil if no subagents are found.
func findSubagents(processTree []string) []model.SubagentInfo {
	var subagents []model.SubagentInfo
	for _, proc := range processTree {
		lower := strings.ToLower(strings.TrimSpace(proc))
		// Match: contains "opencode" AND "-s" AND "ses_"
		if !strings.Contains(lower, "opencode") {
			continue
		}
		if !strings.Contains(proc, "-s") && !strings.Contains(proc, "--session") {
			continue
		}
		// Extract session ID: find "ses_" and take the token
		trimmed := strings.TrimSpace(proc)
		fields := strings.Fields(trimmed)
		for i, f := range fields {
			if strings.HasPrefix(f, "ses_") {
				subagents = append(subagents, model.SubagentInfo{
					SessionID: f,
				})
				break
			}
			// Handle -s ses_xxx (flag and value as separate tokens)
			if (f == "-s" || f == "--session") && i+1 < len(fields) && strings.HasPrefix(fields[i+1], "ses_") {
				subagents = append(subagents, model.SubagentInfo{
					SessionID: fields[i+1],
				})
				break
			}
		}
	}
	return subagents
}
```

**Step 2: Wire into Parse() — two insertion points**

In the `Parse()` method, replace the idle-at-bottom block (lines 40-52) to check subagents before returning idle:

```go
	if p.isIdleAtBottom(content) {
		// Before classifying as idle, check if subagents are running.
		// A parent with active subagents is working, not idle.
		if subs := findSubagents(processTree); len(subs) > 0 {
			return &Result{
				Agent:     "opencode",
				Blocked:   false,
				Reason:    "subagent active",
				Reasoning: "deterministic parser: OpenCode TUI idle at bottom, but process tree contains active subagent(s)",
				Subagents: subs,
				Actions: []model.Action{
					{Keys: "Enter", Label: "send empty message / continue", Risk: "low", Raw: true},
				},
			}
		}
		return &Result{
			Agent:      "opencode",
			Blocked:    true,
			Reason:     "idle at prompt",
			WaitingFor: "idle at prompt",
			Actions: []model.Action{
				{Keys: "Enter", Label: "send empty message / continue", Risk: "low", Raw: true},
			},
			Recommended: 0,
			Reasoning:   "deterministic parser: OpenCode TUI detected, idle prompt at bottom of screen",
		}
	}
```

Also replace the default fallthrough block (lines 74-85) to check subagents:

```go
	// Before falling through to idle, check for subagent processes.
	if subs := findSubagents(processTree); len(subs) > 0 {
		return &Result{
			Agent:     "opencode",
			Blocked:   false,
			Reason:    "subagent active",
			Reasoning: "deterministic parser: OpenCode TUI detected, no active execution indicators, but process tree contains active subagent(s)",
			Subagents: subs,
			Actions: []model.Action{
				{Keys: "Enter", Label: "send empty message / continue", Risk: "low", Raw: true},
			},
		}
	}

	// Default: idle at prompt (fallthrough for unrecognized OpenCode state)
	return &Result{
		Agent:      "opencode",
		Blocked:    true,
		Reason:     "idle at prompt",
		WaitingFor: "idle at prompt",
		Actions: []model.Action{
			{Keys: "Enter", Label: "send empty message / continue", Risk: "low", Raw: true},
		},
		Recommended: 0,
		Reasoning:   "deterministic parser: OpenCode TUI detected, no active execution indicators, agent is idle",
	}
```

**Step 3: Run tests to verify they pass**

Run: `go test ./internal/parser/... -v`
Expected: ALL PASS

**Step 4: Run full test suite and lint**

Run: `just lint && just test && just build`
Expected: All pass

**Step 5: Commit**

```bash
git add internal/parser/opencode.go
git commit -m "feat: detect OpenCode subagents in process tree"
```

---

### Task 6: Final verification

**Step 1: Run full quality checks**

Run: `just lint && just test && just build`
Expected: All pass, zero warnings (except any pre-existing ones)

**Step 2: Verify JSON output includes subagents**

Run: `go build -o bin/pane-patrol . && echo "build ok"`
Expected: build ok
