package parser

import (
	"strings"
	"testing"
)

// --- OpenCode Parser Tests ---

func TestOpenCode_PermissionDialog(t *testing.T) {
	content := `
some previous output...

  △ Permission required

  # Bash command
  $ git diff HEAD~3

  Allow once  Allow always  Reject

  ⇆ select  enter confirm
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result for OpenCode permission dialog")
	}
	if result.Agent != "opencode" {
		t.Errorf("agent: got %q, want %q", result.Agent, "opencode")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for permission dialog")
	}
	if len(result.Actions) < 3 {
		t.Errorf("expected at least 3 actions, got %d", len(result.Actions))
	}
	// All actions should be raw (OpenCode runs in raw mode)
	for i, a := range result.Actions {
		if !a.Raw {
			t.Errorf("action %d (%q): expected Raw=true", i, a.Keys)
		}
	}
	if result.WaitingFor == "" {
		t.Error("expected non-empty waiting_for")
	}
}

func TestOpenCode_RejectDialog(t *testing.T) {
	content := `
  △ Reject permission

  Tell OpenCode what to do differently

  > 
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result for OpenCode reject dialog")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for reject dialog")
	}
	if result.Reason != "reject dialog — waiting for alternative instructions" {
		t.Errorf("unexpected reason: %q", result.Reason)
	}
}

func TestOpenCode_ActiveExecution_Spinner(t *testing.T) {
	content := `
  ▣ Build · claude-sonnet-4-5 · 12s

  ■■■⬝⬝⬝⬝⬝

  esc interrupt
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false during active execution")
	}
}

func TestOpenCode_ActiveExecution_BrailleSpinner(t *testing.T) {
	content := `
  some task ⠹ running...
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false during braille spinner")
	}
}

func TestOpenCode_IdleAtPrompt(t *testing.T) {
	content := `
  Previous conversation output...

  > 
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true when idle at prompt")
	}
	if result.WaitingFor != "idle at prompt" {
		t.Errorf("waiting_for: got %q, want %q", result.WaitingFor, "idle at prompt")
	}
}

func TestOpenCode_NotRecognized(t *testing.T) {
	content := `$ ls -la
total 42
drwxr-xr-x  5 user user 160 Jan  1 00:00 .
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"bash", "zsh"})
	if result != nil {
		t.Error("expected nil result for non-OpenCode content")
	}
}

func TestOpenCode_PermissionOverridesBuild(t *testing.T) {
	// Permission dialog visible even though Build indicator is present
	content := `
  ▣ Build · claude-sonnet-4-5 · 8s

  △ Permission required

  → Read /etc/hosts

  Allow once  Allow always  Reject

  ⇆ select  enter confirm
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true: permission dialog overrides Build indicator")
	}
}

// --- Claude Code Parser Tests ---

func TestClaude_PermissionDialog(t *testing.T) {
	content := `
  Claude needs your permission to use Read

  Read file: /etc/hosts

  Do you want to proceed?
  ❯ 1. Yes  2. Yes, and don't ask again  3. No
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result for Claude permission dialog")
	}
	if result.Agent != "claude_code" {
		t.Errorf("agent: got %q, want %q", result.Agent, "claude_code")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for permission dialog")
	}
	// Should have approve, deny, and "don't ask again" actions
	if len(result.Actions) < 3 {
		t.Errorf("expected at least 3 actions (with don't ask again), got %d", len(result.Actions))
	}
	// First action should be approve via numeric '1' key (raw mode)
	if result.Actions[0].Keys != "1" {
		t.Errorf("first action keys: got %q, want %q", result.Actions[0].Keys, "1")
	}
	if !result.Actions[0].Raw {
		t.Error("first action should be Raw=true for Claude Code")
	}
	// "don't ask again" should be key "2"
	if result.Actions[1].Keys != "2" {
		t.Errorf("second action keys: got %q, want %q", result.Actions[1].Keys, "2")
	}
	// deny should be key "3"
	if result.Actions[2].Keys != "3" {
		t.Errorf("third action keys: got %q, want %q", result.Actions[2].Keys, "3")
	}
	// WaitingFor should show "Read — Read file: /etc/hosts"
	if !strings.Contains(result.WaitingFor, "Read") {
		t.Errorf("WaitingFor should contain tool name, got: %q", result.WaitingFor)
	}
	if !strings.Contains(result.WaitingFor, "/etc/hosts") {
		t.Errorf("WaitingFor should contain file path, got: %q", result.WaitingFor)
	}
	if !strings.Contains(result.WaitingFor, "—") {
		t.Errorf("WaitingFor should contain dash separator, got: %q", result.WaitingFor)
	}
}

func TestClaude_EditApproval(t *testing.T) {
	content := `
  Do you want to make this edit to src/main.go?

  @@ -10,3 +10,4 @@
   import "fmt"
  +import "os"

  Esc to cancel · Tab to amend
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result for edit approval")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for edit approval")
	}
	if result.Actions[0].Keys != "1" {
		t.Errorf("first action keys: got %q, want %q", result.Actions[0].Keys, "1")
	}
	// WaitingFor should show "Edit — src/main.go"
	if result.WaitingFor != "Edit — src/main.go" {
		t.Errorf("WaitingFor: got %q, want %q", result.WaitingFor, "Edit — src/main.go")
	}
}

func TestClaude_AutoResolve(t *testing.T) {
	content := `
  Auto-selecting in 3s… Press any key to intervene.
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result for auto-resolve")
	}
	if result.Blocked {
		t.Error("expected blocked=false during auto-resolve countdown")
	}
}

func TestClaude_ActiveThinking(t *testing.T) {
	// Claude Code shows "✻ <verb>… (time · ↓ tokens)" while working.
	// The verb is randomized (Scampering, Pondering, Reasoning, etc.)
	content := `
  some previous output...

✻ Scampering… (2m 22s · ↓ 2.8k tokens)

❯
? for shortcuts
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result for active thinking")
	}
	if result.Blocked {
		t.Error("expected blocked=false during active thinking (✻ Scampering…)")
	}
}

func TestClaude_ActiveThinkingVariousVerbs(t *testing.T) {
	verbs := []string{"Thinking", "Reasoning", "Pondering", "Scampering", "Planning"}
	p := &ClaudeCodeParser{}
	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			content := "✻ " + verb + "… (1m 5s · ↓ 500 tokens)\n❯\n? for shortcuts"
			result := p.Parse(content, []string{"claude"})
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.Blocked {
				t.Errorf("expected blocked=false during ✻ %s…", verb)
			}
		})
	}
}

func TestClaude_ActiveThinkingAllSpinnerChars(t *testing.T) {
	// Claude Code cycles through multiple indicator characters during thinking.
	// Non-Ghostty: ["·", "✢", "✳", "✶", "✻", "✽"]
	// Ghostty:     ["·", "✢", "✳", "✶", "✻", "*"]
	// All must be recognized as active when followed by verb + ellipsis.
	indicators := []struct {
		char string
		name string
	}{
		{"✢", "U+2722 Four Teardrop-Spoked Asterisk"},
		{"✳", "U+2733 Eight Spoked Asterisk"},
		{"✶", "U+2736 Six Pointed Black Star"},
		{"✻", "U+273B Teardrop-Spoked Asterisk"},
		{"✽", "U+273D Heavy Teardrop-Spoked Asterisk"},
		{"·", "U+00B7 Middle Dot"},
		{"*", "U+002A Asterisk"},
	}
	p := &ClaudeCodeParser{}
	for _, ind := range indicators {
		t.Run(ind.name, func(t *testing.T) {
			content := ind.char + " Gusting… (1m 23s · ↓ 2.8k tokens · thought for 5s)\n\n❯\n? for shortcuts"
			result := p.Parse(content, []string{"claude"})
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.Blocked {
				t.Errorf("expected blocked=false during active thinking with indicator %s (%s)", ind.char, ind.name)
			}
		})
	}
}

func TestClaude_CompletedAllSpinnerChars(t *testing.T) {
	// Completed indicators (no ellipsis) must still be recognized as idle.
	indicators := []string{"·", "✢", "✳", "✶", "✻", "✽", "*"}
	p := &ClaudeCodeParser{}
	for _, ind := range indicators {
		t.Run(ind, func(t *testing.T) {
			content := ind + " Brewed for 39s\n\n❯\n? for shortcuts"
			result := p.Parse(content, []string{"claude"})
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if !result.Blocked {
				t.Errorf("expected blocked=true for completed indicator %s (no ellipsis = idle)", ind)
			}
		})
	}
}

func TestClaude_BulletListNotMistakenForSpinner(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{
			name: "asterisk-bullet",
			content: `* Next steps...

❯
? for shortcuts`,
		},
		{
			name: "middle-dot-bullet",
			content: `· Next steps…

❯
? for shortcuts`,
		},
	}

	p := &ClaudeCodeParser{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := p.Parse(tc.content, []string{"claude"})
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if !result.Blocked {
				t.Fatalf("expected blocked=true for markdown-style bullet line, got blocked=false (reason=%q)", result.Reason)
			}
		})
	}
}

func TestClaude_CompletedNotActive(t *testing.T) {
	// Completed verbs are randomized (8 possible: Baked, Brewed, Churned,
	// Cogitated, Cooked, Crunched, Sautéed, Worked). All should be treated
	// as idle — the key is the ABSENCE of "…" (ellipsis).
	completedVerbs := []string{
		"Baked", "Brewed", "Churned", "Cogitated",
		"Cooked", "Crunched", "Sautéed", "Worked",
	}
	p := &ClaudeCodeParser{}
	for _, verb := range completedVerbs {
		t.Run(verb, func(t *testing.T) {
			content := "  Task completed successfully.\n\n✻ " + verb + " for 3m 10s\n\n❯\n? for shortcuts"
			result := p.Parse(content, []string{"claude"})
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if !result.Blocked {
				t.Errorf("expected blocked=true after completion (✻ %s for = idle)", verb)
			}
		})
	}
}

func TestClaude_IdleIndicator(t *testing.T) {
	// "✻ Idle" is a distinct idle state — no verb, no ellipsis, no duration.
	// Should be treated as blocked (idle at prompt).
	content := `
  Task completed successfully.

✻ Idle

❯
? for shortcuts
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for '✻ Idle' state")
	}
	if result.Reason != "idle at prompt" {
		t.Errorf("reason: got %q, want %q", result.Reason, "idle at prompt")
	}
}

func TestClaude_IdleAtPrompt(t *testing.T) {
	content := `
  Claude finished the task.

  >

  Esc to cancel · Tab to amend
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true when idle at prompt")
	}
}

func TestClaude_NotRecognized(t *testing.T) {
	content := `$ npm install
added 42 packages in 3s
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"node", "npm"})
	if result != nil {
		t.Error("expected nil result for non-Claude content")
	}
}

func TestClaude_IdentifiedByShortcutsFooter(t *testing.T) {
	// When there's no "claude" in the process tree and no permission dialog,
	// "? for shortcuts" footer alone should identify this as Claude Code.
	content := `
  Previous task output here...

✻ Worked for 2m 15s

❯
? for shortcuts
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"node", "bun"}) // no "claude" in process tree
	if result == nil {
		t.Fatal("expected non-nil result: '? for shortcuts' should identify Claude Code")
	}
	if result.Agent != "claude_code" {
		t.Errorf("agent: got %q, want %q", result.Agent, "claude_code")
	}
	if !result.Blocked {
		t.Error("expected blocked=true (completed work, idle at prompt)")
	}
}

func TestClaude_IdentifiedByThinkingIndicator(t *testing.T) {
	// The "✻" indicator alone (without process tree or other markers)
	// should identify this as Claude Code.
	content := `
✻ Reasoning… (45s · ↓ 1.2k tokens)
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"node"}) // no "claude" in process tree
	if result == nil {
		t.Fatal("expected non-nil result: '✻' should identify Claude Code")
	}
	if result.Agent != "claude_code" {
		t.Errorf("agent: got %q, want %q", result.Agent, "claude_code")
	}
	if result.Blocked {
		t.Error("expected blocked=false (actively thinking)")
	}
}

func TestClaude_IdentifiedByAmbiguousThinkingIndicators(t *testing.T) {
	indicators := []string{"·", "*"}
	p := &ClaudeCodeParser{}
	for _, ind := range indicators {
		t.Run(ind, func(t *testing.T) {
			content := ind + " Reasoning… (45s · ↓ 1.2k tokens)"
			result := p.Parse(content, []string{"node"}) // no "claude" in process tree
			if result == nil {
				t.Fatal("expected non-nil result for ambiguous spinner indicator fallback")
			}
			if result.Agent != "claude_code" {
				t.Fatalf("agent: got %q, want %q", result.Agent, "claude_code")
			}
			if result.Blocked {
				t.Fatal("expected blocked=false (actively thinking)")
			}
		})
	}
}

// --- Codex Parser Tests ---

func TestCodex_ExecApproval(t *testing.T) {
	content := `
  Would you like to run the following command?

  Reason: Need to check git history
  $ git log --oneline -10

  Yes, proceed
  Yes, and don't ask again for commands that start with ` + "`git`" + `
  No, and tell Codex what to do differently
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result for Codex exec approval")
	}
	if result.Agent != "codex" {
		t.Errorf("agent: got %q, want %q", result.Agent, "codex")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for exec approval")
	}
	if len(result.Actions) < 3 {
		t.Errorf("expected at least 3 actions, got %d", len(result.Actions))
	}
	// First action: Enter to approve (raw mode)
	if result.Actions[0].Keys != "Enter" {
		t.Errorf("first action keys: got %q, want %q", result.Actions[0].Keys, "Enter")
	}
	if !result.Actions[0].Raw {
		t.Error("first action should be Raw=true for Codex")
	}
}

func TestCodex_EditApproval(t *testing.T) {
	content := `
  Would you like to make the following edits?

  --- a/src/main.go
  +++ b/src/main.go

  Yes, proceed
  No, and tell Codex what to do differently
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result for edit approval")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for edit approval")
	}
}

func TestCodex_NetworkApproval(t *testing.T) {
	content := `
  Do you want to approve access to "api.github.com"?

  Yes, just this once
  Yes, and allow this host for this session
  No, and tell Codex what to do differently
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result for network approval")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for network approval")
	}
	if result.Reason != "network access approval dialog" {
		t.Errorf("unexpected reason: %q", result.Reason)
	}
}

func TestCodex_MCPApproval(t *testing.T) {
	content := `
  filesystem-server needs your approval.

  Allow access to /home/user/project
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result for MCP approval")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for MCP approval")
	}
}

func TestCodex_ActiveWorking(t *testing.T) {
	content := `
  Working

  └ Reading file src/main.go

  (12s · esc to interrupt)
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false during active working")
	}
}

func TestCodex_IdleAtPrompt(t *testing.T) {
	content := `
  Task completed successfully.

  Plan mode  shift+tab to cycle

  >
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true when idle at prompt")
	}
}

func TestCodex_IdentifiedBySplashBanner(t *testing.T) {
	// Codex at idle shows ">_ OpenAI Codex" splash and "? for shortcuts" footer.
	// Should be identified as Codex, NOT Claude Code (which also has "? for shortcuts").
	content := `
│ >_ OpenAI Codex (v0.104.0)                  │
│                                             │
│ model:     gpt-5.3-codex   /model to change │
│ directory: /tmp                             │
╰─────────────────────────────────────────────╯
  Tip: New 2x rate limits until April 2nd.
› Run /review on my current changes
  ? for shortcuts                                                                                     100% context left
`
	r := NewRegistry()
	result := r.Parse(content, []string{"node"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Agent != "codex" {
		t.Errorf("agent: got %q, want %q — Codex splash banner should identify as Codex, not Claude", result.Agent, "codex")
	}
	if !result.Blocked {
		t.Error("expected blocked=true (idle at prompt)")
	}
}

func TestCodex_NotRecognized(t *testing.T) {
	content := `$ python main.py
Hello, world!
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"python"})
	if result != nil {
		t.Error("expected nil result for non-Codex content")
	}
}

func TestCodex_UserInputRequest(t *testing.T) {
	content := `
  What is your preferred database?

  Yes, provide the requested info
  No, but continue without it
  Cancel this request
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result for user input request")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for user input request")
	}
}

// --- Scrollback False-Positive Tests ---
//
// These tests verify that stale active-execution indicators in scrollback
// do NOT override a clearly idle/blocked state at the bottom of the screen.
// tmux capture-pane returns the visible viewport by default, but long-running
// agents may have prior "Working/✻ Thinking/Build" lines still visible above
// the current prompt.

func TestClaude_StaleThinkingInScrollback(t *testing.T) {
	// Stale "✻ Reasoning…" from a previous turn is still visible in scrollback,
	// but Claude has since completed and is now idle at prompt.
	content := `
✻ Reasoning… (1m 5s · ↓ 500 tokens)

  Here is the answer to your question...
  I've made the changes you requested.

✻ Worked for 2m 15s

❯
? for shortcuts
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true: stale '✻ Reasoning…' in scrollback should not override idle prompt at bottom")
	}
}

func TestClaude_StaleProgressInScrollback(t *testing.T) {
	// Stale "Reading…" progress line from prior tool use visible above
	// a permission dialog at the bottom.
	content := `
  Reading file...

  Claude needs your permission to use Bash

  $ rm -rf /tmp/test

  Do you want to proceed?
  ❯ 1. Yes  2. Yes, and don't ask again  3. No
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true: permission dialog at bottom should take priority over stale progress")
	}
}

func TestOpenCode_StaleBuildInScrollback(t *testing.T) {
	// Stale "▣ Build" from a previous turn visible in scrollback, with enough
	// output below to push it above the bottom non-empty lines window
	// (see bottomLines const in parser.go).
	content := `
  ▣ Build · claude-sonnet-4-5 · 45s

  ■■■■■■■■

  Build completed. Here are the results...
  Line 1 of output.
  Line 2 of output.
  Line 3 of output.
  Line 4 of output.
  Line 5 of output.
  Line 6 of output.

  > 
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true: stale '▣ Build' in scrollback should not override idle prompt at bottom")
	}
}

func TestOpenCode_StaleSpinnerInScrollback(t *testing.T) {
	// Stale braille spinner from a previous operation above a permission dialog.
	content := `
  Processing ⠹ task completed

  △ Permission required

  # Bash command
  $ npm install

  Allow once  Allow always  Reject

  ⇆ select  enter confirm
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true: permission dialog should take priority over stale spinner in scrollback")
	}
}

func TestCodex_StaleWorkingInScrollback(t *testing.T) {
	// Stale "Working" from a previous turn visible in scrollback, with enough
	// output below it that the stale indicators are pushed above the bottom
	// non-empty lines window (see bottomLines const in parser.go).
	content := `
  Working

  └ Reading file src/main.go

  (12s · esc to interrupt)

  Task completed. Made the requested changes.
  Here is line 1 of the output.
  Here is line 2 of the output.
  Here is line 3 of the output.
  Here is line 4 of the output.
  Here is line 5 of the output.

  Plan mode  shift+tab to cycle

  >
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true: stale 'Working' in scrollback should not override idle prompt at bottom")
	}
}

func TestCodex_StaleApprovalAboveIdle(t *testing.T) {
	// Stale "✔ You approved codex to run" from a previous action visible in
	// scrollback, with enough output below to push it above the bottom
	// non-empty lines window (see bottomLines const in parser.go).
	content := `
  ✔ You approved codex to run git status

  Here is the status output...
  On branch main
  Your branch is up to date with 'origin/main'.
  Changes not staged for commit:
    modified: src/main.go
  no changes added to commit

  Plan mode  shift+tab to cycle

  >
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true: stale '✔ You approved' should not override idle prompt at bottom")
	}
}

// --- Idle/Active Coexistence Tests ---
// These tests verify that when both idle and active indicators appear in the
// bottom 8 lines, active wins. This differs from the scrollback tests above
// where active indicators were far above the bottom window.

func TestCodex_WorkingWithPlanModeFooter(t *testing.T) {
	// Codex "Working" + "Plan mode shift+tab" coexist in the bottom lines.
	// The footer is persistent; active should win.
	content := `Working

  └ Reading file src/main.go

  (12s · esc to interrupt)

  Plan mode  shift+tab to cycle
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false: 'Working' in bottom lines should override 'Plan mode' footer")
	}
	if result.Reason != "actively working" {
		t.Errorf("reason: got %q, want %q", result.Reason, "actively working")
	}
}

func TestClaude_FetchingWithShortcutsFooter(t *testing.T) {
	// Claude Code "Fetching…" + "? for shortcuts" coexist in the bottom lines.
	// Active tool progress should override the idle footer.
	content := `Some previous output
✻ Worked for 2m 10s

Fetching https://api.example.com/data…

? for shortcuts
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false: 'Fetching…' in bottom lines should override '? for shortcuts' footer")
	}
	if result.Reason != "actively executing" {
		t.Errorf("reason: got %q, want %q", result.Reason, "actively executing")
	}
}

func TestClaude_SearchingWithShortcutsFooter(t *testing.T) {
	// Claude Code "Searching: ..." + "? for shortcuts" coexist in the bottom lines.
	// The colon suffix indicates streaming output — active, not idle.
	content := `Some previous output
✻ Worked for 2m 10s

Searching: found 3 results in src/

? for shortcuts
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false: 'Searching:' in bottom lines should override '? for shortcuts' footer")
	}
	if result.Reason != "actively executing" {
		t.Errorf("reason: got %q, want %q", result.Reason, "actively executing")
	}
}

func TestClaude_ThinkingWithPrompt(t *testing.T) {
	// Claude Code "✻ Pondering…" appears near the bottom alongside the prompt.
	// This can happen briefly during state transitions.
	content := `Some output

✻ Pondering… (30s · ↓ 1.2k tokens)

❯
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false: active '✻ Pondering…' should override idle prompt")
	}
}

func TestOpenCode_SpinnerWithPrompt(t *testing.T) {
	// OpenCode braille spinner coexists with ">" prompt in the bottom lines.
	content := `Some output

⠋ Running task...

>
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false: braille spinner in bottom lines should override idle prompt")
	}
}

func TestOpenCode_EscInterruptWithPrompt(t *testing.T) {
	// OpenCode "esc interrupt" status bar coexists with ">" prompt.
	content := `Some output

esc interrupt

>
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false: 'esc interrupt' should override idle prompt")
	}
}

// --- Stale Dialog in Scrollback/Output Tests ---
// These tests verify that dialog trigger strings appearing in scrollback or in
// the agent's own output text are ignored when the bottom of the screen shows
// a clear idle prompt.

func TestClaude_StalePermissionInAgentOutput(t *testing.T) {
	// The agent wrote output that discusses permission dialogs. The trigger
	// strings "Do you want to proceed?" and "Claude needs your permission"
	// appear as quoted text in the output, not as a live dialog.
	content := `  The parser checks for "Claude needs your permission" in the content.
  When the header has scrolled off, it scans backwards from "Do you want to proceed?"
  collecting up to 6 non-empty lines.
  Some more analysis text here.
  Another line of output.
  And the conclusion of the review.

❯
? for shortcuts
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true (idle at prompt)")
	}
	if result.Reason != "idle at prompt" {
		t.Errorf("reason: got %q, want %q — stale dialog text in output should be ignored", result.Reason, "idle at prompt")
	}
}

func TestClaude_StalePermissionDialogInScrollback(t *testing.T) {
	// A previous permission dialog was approved and the agent has returned
	// to the idle prompt. The dialog text is still visible in scrollback.
	content := `  Claude needs your permission to use Bash

  $ rm -rf /tmp/old-build

  Do you want to proceed?
  ❯ 1. Yes  2. Yes, and don't ask again  3. No

  ✻ Worked for 45s

  Cleaned up the old build artifacts. Ready for next task.
  Here is what I did:
  1. Removed /tmp/old-build directory
  2. Verified no remaining files

❯
? for shortcuts
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true (idle at prompt)")
	}
	if result.Reason != "idle at prompt" {
		t.Errorf("reason: got %q, want %q — stale permission dialog should be ignored", result.Reason, "idle at prompt")
	}
}

func TestCodex_StaleExecApprovalInScrollback(t *testing.T) {
	// Codex previously showed a command approval dialog, it was approved,
	// and the agent has returned to idle. The dialog text is in scrollback.
	content := `  Would you like to run the following command?

  $ npm test

  Yes, proceed   Yes, and don't ask again   No

  ✔ You approved codex to run npm test
  All tests passed.
  Here are the results:
  5 test suites, 42 tests passed
  Coverage: 87%

  Plan mode  shift+tab to cycle

  >
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true (idle at prompt)")
	}
	if result.Reason != "idle at prompt" {
		t.Errorf("reason: got %q, want %q — stale approval dialog should be ignored", result.Reason, "idle at prompt")
	}
}

func TestOpenCode_StalePermissionInScrollback(t *testing.T) {
	// OpenCode previously showed a permission dialog, it was approved, and
	// the agent has returned to idle. The dialog text is in scrollback with
	// enough output below to push dialog indicators above the bottom
	// non-empty lines window (see bottomLines const in parser.go).
	content := `  △ Permission required

  # Bash command
  $ git diff HEAD~3

  Allow once  Allow always  Reject
  ⇆ select  enter confirm
  Permission granted.
  Here is the diff output:
  + added new feature
  - removed old code
  + another addition
  - another removal
  Changes verified.
  All looks good.
  Ready for next task.

  >
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true (idle at prompt)")
	}
	if result.Reason != "idle at prompt" {
		t.Errorf("reason: got %q, want %q — stale permission dialog should be ignored", result.Reason, "idle at prompt")
	}
}

// --- Registry Tests ---

func TestRegistry_MatchesOpenCode(t *testing.T) {
	r := NewRegistry()
	content := `△ Permission required
  $ rm -rf /tmp/test
  Allow once  Allow always  Reject
  ⇆ select  enter confirm`

	result := r.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected registry to match OpenCode")
	}
	if result.Agent != "opencode" {
		t.Errorf("agent: got %q, want %q", result.Agent, "opencode")
	}
}

func TestRegistry_MatchesClaudeCode(t *testing.T) {
	r := NewRegistry()
	content := `Claude needs your permission to use Bash
  Do you want to proceed?
  ❯ 1. Yes  2. Yes, and don't ask again  3. No`

	result := r.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected registry to match Claude Code")
	}
	if result.Agent != "claude_code" {
		t.Errorf("agent: got %q, want %q", result.Agent, "claude_code")
	}
}

func TestRegistry_MatchesCodex(t *testing.T) {
	r := NewRegistry()
	content := `Would you like to run the following command?
  $ ls -la`

	result := r.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected registry to match Codex")
	}
	if result.Agent != "codex" {
		t.Errorf("agent: got %q, want %q", result.Agent, "codex")
	}
}

func TestRegistry_NoMatch(t *testing.T) {
	r := NewRegistry()
	content := `$ htop
  PID USER      PR  NI    VIRT    RES    SHR S  %CPU  %MEM     TIME+ COMMAND`

	result := r.Parse(content, []string{"htop"})
	if result != nil {
		t.Errorf("expected nil result for htop, got agent=%q", result.Agent)
	}
}

// --- extractBlock Tests ---

func TestExtractBlock(t *testing.T) {
	content := `some previous output

△ Permission required
# Bash command
$ git diff HEAD~3

Allow once  Allow always  Reject

more stuff after`

	block := extractBlock(content, "△ Permission required")
	if block == "" {
		t.Fatal("expected non-empty block")
	}
	if !strings.Contains(block, "△ Permission required") {
		t.Error("block should contain the marker")
	}
	if !strings.Contains(block, "git diff") {
		t.Error("block should contain the command")
	}
}

func TestClaude_PermissionBashCommand(t *testing.T) {
	// Full Bash permission dialog with tool name and command visible.
	content := `
  Claude needs your permission to use Bash

  $ git -C /home/user/project log --oneline -10

  Do you want to proceed?
  ❯ 1. Yes  2. Yes, and don't ask again  3. No
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Should show "Bash — $ git -C /home/user/project log --oneline -10"
	if !strings.Contains(result.WaitingFor, "Bash") {
		t.Errorf("WaitingFor should start with tool name, got: %q", result.WaitingFor)
	}
	if !strings.Contains(result.WaitingFor, "—") {
		t.Errorf("WaitingFor should contain separator, got: %q", result.WaitingFor)
	}
	if !strings.Contains(result.WaitingFor, "git -C") {
		t.Errorf("WaitingFor should contain the command, got: %q", result.WaitingFor)
	}
}

func TestClaude_PermissionScrolledOff(t *testing.T) {
	// When "Claude needs your permission" has scrolled off, only
	// "Do you want to proceed?" is visible. The WaitingFor should
	// still include context lines above it (tool name, command).
	content := `
  $ git log --oneline -10
  Working directory: /home/user/project

  Do you want to proceed?
  ❯ 1. Yes  2. Yes, and don't ask again  3. No

? for shortcuts
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !strings.Contains(result.WaitingFor, "git log") {
		t.Errorf("WaitingFor should include context above 'Do you want to proceed?', got:\n%s", result.WaitingFor)
	}
}

// --- OpenCode Question Dialog Tests ---

func TestOpenCode_QuestionDialogSingleQuestion(t *testing.T) {
	// OpenCode question tool: single question with numbered options.
	// Source: packages/opencode/src/cli/cmd/tui/routes/session/question.tsx
	// Real content has ┃ border prefix on every dialog line (SplitBorder component).
	content := `
  ┃
  ┃  Which database should I use?
  ┃
  ┃  1. PostgreSQL
  ┃     Best for complex queries
  ┃  2. SQLite
  ┃     Good for embedded use
  ┃  3. Type your own answer
  ┃
  ┃  ↑↓ select  enter submit  esc dismiss
  ┃
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result for question dialog")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for question dialog")
	}
	if result.Reason != "question dialog waiting for answer" {
		t.Errorf("reason: got %q, want %q", result.Reason, "question dialog waiting for answer")
	}
	if !strings.Contains(result.WaitingFor, "database") {
		t.Errorf("WaitingFor should contain question text, got: %q", result.WaitingFor)
	}
	// Should have actions for 3 options + dismiss
	if len(result.Actions) < 4 {
		t.Errorf("expected at least 4 actions (3 options + dismiss), got %d", len(result.Actions))
	}
	// First action should be "1" key
	if result.Actions[0].Keys != "1" {
		t.Errorf("first action keys: got %q, want %q", result.Actions[0].Keys, "1")
	}
	// Last action should be Escape
	lastAction := result.Actions[len(result.Actions)-1]
	if lastAction.Keys != "Escape" {
		t.Errorf("last action keys: got %q, want %q", lastAction.Keys, "Escape")
	}
	// WaitingFor should contain option descriptions (stripped of ┃ prefix)
	if !strings.Contains(result.WaitingFor, "Best for complex queries") {
		t.Errorf("WaitingFor should contain description, got: %q", result.WaitingFor)
	}
}

func TestOpenCode_QuestionDialogMultiQuestion(t *testing.T) {
	// Multi-question form with tab-style headers.
	// Source: packages/opencode/src/cli/cmd/tui/routes/session/question.tsx
	content := `
  ┃   Database      Config      Confirm
  ┃
  ┃  Which database should I use?
  ┃
  ┃  1. PostgreSQL
  ┃  2. SQLite
  ┃
  ┃  ⇆ tab  ↑↓ select  enter confirm  esc dismiss
  ┃
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result for multi-question dialog")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for multi-question dialog")
	}
	if result.Reason != "question dialog waiting for answer" {
		t.Errorf("reason: got %q, want %q", result.Reason, "question dialog waiting for answer")
	}
}

func TestOpenCode_QuestionNotOverriddenByIdlePrompt(t *testing.T) {
	// Question footer "↑↓ select" should prevent idle detection even
	// when ">" prompt is visible in the bottom lines.
	content := `
  ┃  Pick a framework
  ┃
  ┃  1. React
  ┃  2. Vue
  ┃
  ┃  ↑↓ select  enter submit  esc dismiss

  >
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Reason != "question dialog waiting for answer" {
		t.Errorf("reason: got %q, want %q — question dialog should override idle prompt",
			result.Reason, "question dialog waiting for answer")
	}
}

func TestOpenCode_StaleQuestionInScrollback(t *testing.T) {
	// Stale question dialog text in scrollback, agent now idle at prompt.
	// The question footer has scrolled above the bottom 8 lines window.
	content := `
  ┃  Pick a framework
  ┃
  ┃  1. React
  ┃  2. Vue
  ┃
  ┃  ↑↓ select  enter submit  esc dismiss

  Selected React. Proceeding with React setup.
  Installing dependencies...
  Setup complete. Here's what I did:
  1. Created package.json
  2. Installed React and ReactDOM
  3. Created src/App.jsx

  >
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true")
	}
	if result.Reason != "idle at prompt" {
		t.Errorf("reason: got %q, want %q — stale question should be ignored",
			result.Reason, "idle at prompt")
	}
}

func TestOpenCode_QuestionDialogMultiSelect(t *testing.T) {
	// Multi-select question with [✓]/[ ] prefixes.
	// Source: packages/opencode/src/cli/cmd/tui/routes/session/question.tsx
	content := `
  ┃  Which features do you need? (select all that apply)
  ┃
  ┃  1. [✓] Authentication
  ┃  2. [ ] Database
  ┃  3. [ ] API routes
  ┃  4. Type your own answer
  ┃
  ┃  ↑↓ select  enter toggle  esc dismiss
  ┃
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result for multi-select question")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for multi-select question")
	}
	if result.Reason != "question dialog waiting for answer" {
		t.Errorf("reason: got %q, want %q", result.Reason, "question dialog waiting for answer")
	}
}

// --- Codex Question Dialog Tests ---

func TestCodex_QuestionDialogSingle(t *testing.T) {
	// Codex RequestUserInputOverlay: single question with numbered options.
	// Source: codex-rs/tui/src/bottom_pane/request_user_input/mod.rs
	content := `
  What is your preferred testing framework?

  › 1. Jest
    Popular JavaScript testing framework.
    2. Vitest
    Fast Vite-native testing framework.
    3. None of the above
    Optionally, add details in notes (tab).

  enter to submit answer | esc to interrupt
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result for Codex question dialog")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for question dialog")
	}
	if result.Reason != "question dialog waiting for answer" {
		t.Errorf("reason: got %q, want %q", result.Reason, "question dialog waiting for answer")
	}
	if !strings.Contains(result.WaitingFor, "testing framework") {
		t.Errorf("WaitingFor should contain question text, got: %q", result.WaitingFor)
	}
}

func TestCodex_QuestionDialogMultiQuestion(t *testing.T) {
	// Multi-question form with "enter to submit all" footer.
	content := `
  Choose a database engine.

  › 1. PostgreSQL
    Robust relational database.
    2. SQLite
    Embedded database.

  enter to submit all | esc to interrupt
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result for multi-question dialog")
	}
	if result.Reason != "question dialog waiting for answer" {
		t.Errorf("reason: got %q, want %q", result.Reason, "question dialog waiting for answer")
	}
}

func TestCodex_QuestionNotOverriddenByIdlePrompt(t *testing.T) {
	// Question footer should prevent idle detection.
	content := `
  Pick a tool.

  › 1. Webpack
    2. Vite

  enter to submit answer | esc to interrupt

  >
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Reason != "question dialog waiting for answer" {
		t.Errorf("reason: got %q, want %q — question dialog should override idle prompt",
			result.Reason, "question dialog waiting for answer")
	}
}

func TestCodex_StaleQuestionInScrollback(t *testing.T) {
	// Stale question dialog in scrollback, agent now idle at prompt.
	content := `
  What is your preferred testing framework?

  › 1. Jest
    2. Vitest

  enter to submit answer | esc to interrupt

  Selected Jest. Setting up test configuration.
  Created jest.config.js
  Added test scripts to package.json
  Ready for next task.

  Plan mode  shift+tab to cycle

  >
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Reason != "idle at prompt" {
		t.Errorf("reason: got %q, want %q — stale question should be ignored",
			result.Reason, "idle at prompt")
	}
}

func TestCodex_QuestionDialogFreeform(t *testing.T) {
	// Freeform question with no options (just notes input).
	// Source: mod.rs — when options is None/empty, focus goes to Notes.
	content := `
  What should the project name be?

  enter to submit answer | esc to interrupt
`
	p := &CodexParser{}
	result := p.Parse(content, []string{"codex"})
	if result == nil {
		t.Fatal("expected non-nil result for freeform question")
	}
	if !result.Blocked {
		t.Error("expected blocked=true for freeform question")
	}
	if result.Reason != "question dialog waiting for answer" {
		t.Errorf("reason: got %q, want %q", result.Reason, "question dialog waiting for answer")
	}
}

// --- Shared Helper Tests ---

func TestExtractQuestionSummary(t *testing.T) {
	// Realistic OpenCode content with ┃ border prefix
	lines := strings.Split(`
  ┃  Which database should I use?
  ┃
  ┃  1. PostgreSQL
  ┃     Best for complex queries
  ┃  2. SQLite
  ┃     Good for embedded use
  ┃  3. Type your own answer
  ┃
  ┃  ↑↓ select  enter submit  esc dismiss
`, "\n")
	summary := extractQuestionSummary(lines)
	if !strings.Contains(summary, "database") {
		t.Errorf("summary should contain question text, got: %q", summary)
	}
	if !strings.Contains(summary, "1. PostgreSQL") {
		t.Errorf("summary should contain first option, got: %q", summary)
	}
	if !strings.Contains(summary, "2. SQLite") {
		t.Errorf("summary should contain second option, got: %q", summary)
	}
	// Description lines should be included (indented under options)
	if !strings.Contains(summary, "Best for complex queries") {
		t.Errorf("summary should contain option description, got: %q", summary)
	}
	if !strings.Contains(summary, "Good for embedded use") {
		t.Errorf("summary should contain second option description, got: %q", summary)
	}
	// ┃ border should be stripped from output
	if strings.Contains(summary, "┃") {
		t.Errorf("summary should not contain ┃ border, got: %q", summary)
	}
}

func TestExtractQuestionSummaryCodexStyle(t *testing.T) {
	// Codex renders options with "› " cursor prefix
	lines := strings.Split(`
  What framework do you want?

  › 1. React
    Build interactive UIs
    2. Vue
    Progressive framework
    3. None of the above

  enter to submit answer  esc to interrupt
`, "\n")
	summary := extractQuestionSummary(lines)
	if !strings.Contains(summary, "framework") {
		t.Errorf("summary should contain question text, got: %q", summary)
	}
	if !strings.Contains(summary, "1. React") {
		t.Errorf("summary should contain first option (with or without ›), got: %q", summary)
	}
	if !strings.Contains(summary, "2. Vue") {
		t.Errorf("summary should contain second option, got: %q", summary)
	}
}

func TestExtractQuestionSummaryNoOptions(t *testing.T) {
	lines := strings.Split(`
  Some random content
  No numbered options here
`, "\n")
	summary := extractQuestionSummary(lines)
	if summary != "question dialog" {
		t.Errorf("expected fallback 'question dialog', got: %q", summary)
	}
}

func TestExtractQuestionSummaryStopsAtFooter(t *testing.T) {
	// Descriptions should not cross into footer lines.
	// Uses ┃ border prefix like real OpenCode content.
	lines := strings.Split(`
  ┃  Pick a color
  ┃
  ┃  1. Red
  ┃     Warm color
  ┃  2. Blue
  ┃     Cool color
  ┃  ↑↓ select  enter confirm  esc dismiss
`, "\n")
	summary := extractQuestionSummary(lines)
	// Footer should NOT appear in the summary
	if strings.Contains(summary, "↑↓ select") {
		t.Errorf("summary should not contain footer, got: %q", summary)
	}
	if !strings.Contains(summary, "Warm color") {
		t.Errorf("summary should contain description, got: %q", summary)
	}
}

func TestExtractQuestionSummaryRealisticMultiSelect(t *testing.T) {
	// Realistic multi-select question from a live OpenCode session.
	// Every dialog line has ┃ border prefix + multi-select [ ] checkboxes.
	lines := strings.Split(`
  ┃   Next steps   Aspire wt config   Skill overlap   Confirm
  ┃
  ┃  Now that superpowers is installed, what would you like to do next? (select all that apply)
  ┃
  ┃  1. [ ] Configure wt for multi-repo
  ┃     Set up the custom pattern on both machines for cross-repo task grouping
  ┃  2. [ ] Add wt hooks
  ┃     Configure post_create/post_checkout hooks for automatic dependency installation
  ┃  3. [ ] Update global AGENTS.md
  ┃     Align ~/.config/opencode/AGENTS.md to reference superpowers
  ┃  4. [ ] Test the installation
  ┃     Verify skills are discoverable and the wt integration works
  ┃  5. [ ] Something else
  ┃     I have a different task in mind
  ┃  6. [ ] Type your own answer
  ┃
  ┃  ⇆ tab  ↑↓ select  enter toggle  esc dismiss
  ┃
`, "\n")
	summary := extractQuestionSummary(lines)
	if !strings.Contains(summary, "superpowers") {
		t.Errorf("summary should contain question text, got: %q", summary)
	}
	if !strings.Contains(summary, "1. [ ] Configure wt") {
		t.Errorf("summary should contain first option, got: %q", summary)
	}
	if !strings.Contains(summary, "Set up the custom pattern") {
		t.Errorf("summary should contain first option description, got: %q", summary)
	}
	if !strings.Contains(summary, "4. [ ] Test the installation") {
		t.Errorf("summary should contain fourth option, got: %q", summary)
	}
	// ┃ border should be stripped
	if strings.Contains(summary, "┃") {
		t.Errorf("summary should not contain ┃ border, got: %q", summary)
	}
	// Footer should not be included
	if strings.Contains(summary, "⇆ tab") {
		t.Errorf("summary should not contain footer, got: %q", summary)
	}
}

func TestIsFooterLine(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"⇆ select  enter confirm", true},
		{"↑↓ select  enter confirm  esc dismiss", true},
		{"⇆ tab  ↑↓ select", true},
		{"esc dismiss", true},
		{"enter confirm", true},
		{"enter to submit answer", true},
		{"enter to submit all", true},
		{"esc to interrupt", true},
		{"tab to add notes", true},
		{"1. PostgreSQL", false},
		{"Best for complex queries", false},
		{"Which database?", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isFooterLine(tt.input)
			if got != tt.want {
				t.Errorf("isFooterLine(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripDialogPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"┃  1. PostgreSQL", "1. PostgreSQL"},
		{"┃     Description text", "Description text"},
		{"┃  ↑↓ select  enter confirm", "↑↓ select  enter confirm"},
		{"› 1. Jest", "1. Jest"},
		{"›  2. Vitest", "2. Vitest"},
		{"1. Plain option", "1. Plain option"},
		{"Just text", "Just text"},
		{"", ""},
		{"┃  ┃  double border", "double border"}, // unlikely but handled
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripDialogPrefix(tt.input)
			if got != tt.want {
				t.Errorf("stripDialogPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountNumberedOptions(t *testing.T) {
	// With ┃ border prefix (realistic OpenCode content)
	lines := strings.Split(`
  ┃  1. Option A
  ┃     Description
  ┃  2. Option B
  ┃     Description
  ┃  3. Type your own answer
`, "\n")
	count := countNumberedOptions(lines)
	if count != 3 {
		t.Errorf("expected 3 options, got %d", count)
	}

	// Without border prefix (plain content)
	plain := strings.Split(`
  1. Option A
  2. Option B
`, "\n")
	count2 := countNumberedOptions(plain)
	if count2 != 2 {
		t.Errorf("expected 2 options for plain content, got %d", count2)
	}
}

func TestIsNumberedOption(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1. PostgreSQL", true},
		{"2. SQLite", true},
		{"9. Last option", true},
		{"1.Created package.json", true}, // edge case: no space after period
		{"0. Zero", false},               // starts at 1, not 0
		{"A. Alpha", false},
		{"", false},
		{"1", false},
		{"Just text", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isNumberedOption(tt.input)
			if got != tt.want {
				t.Errorf("isNumberedOption(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseTabHeaders(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []string
	}{
		{
			name: "realistic OpenCode multi-tab",
			lines: strings.Split(`
  ┃   Next steps   Aspire wt config   Skill overlap   Confirm
  ┃
  ┃  Which features? (select all that apply)
  ┃
  ┃  1. [ ] Option A
  ┃  2. [ ] Option B
  ┃
  ┃  ⇆ tab  ↑↓ select  enter toggle  esc dismiss
`, "\n"),
			want: []string{"Next steps", "Aspire wt config", "Skill overlap", "Confirm"},
		},
		{
			name: "two tabs plus confirm",
			lines: strings.Split(`
  ┃   Database   Config   Confirm
  ┃
  ┃  Which database?
`, "\n"),
			want: []string{"Database", "Config", "Confirm"},
		},
		{
			name: "no tabs single question",
			lines: strings.Split(`
  ┃  Which database?
  ┃
  ┃  1. PostgreSQL
  ┃  2. SQLite
  ┃
  ┃  ↑↓ select  enter submit  esc dismiss
`, "\n"),
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTabHeaders(tt.lines)
			if tt.want == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("expected %d tabs, got %d: %v", len(tt.want), len(got), got)
				return
			}
			for i, w := range tt.want {
				if got[i] != w {
					t.Errorf("tab[%d]: got %q, want %q", i, got[i], w)
				}
			}
		})
	}
}

func TestSplitTabSegments(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"Next steps   Aspire wt config   Skill overlap   Confirm",
			[]string{"Next steps", "Aspire wt config", "Skill overlap", "Confirm"}},
		{"Database   Config   Confirm",
			[]string{"Database", "Config", "Confirm"}},
		{"Single tab only", nil}, // only 1 segment
		{"A   B", []string{"A", "B"}},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitTabSegments(tt.input)
			if tt.want == nil {
				if len(got) >= 2 {
					t.Errorf("expected <2 segments, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("expected %d segments, got %d: %v", len(tt.want), len(got), got)
				return
			}
			for i, w := range tt.want {
				if got[i] != w {
					t.Errorf("segment[%d]: got %q, want %q", i, got[i], w)
				}
			}
		})
	}
}

func TestOpenCode_QuestionDialogMultiTab(t *testing.T) {
	// Multi-question form with tab headers and ⇆ tab footer.
	// Source: packages/opencode/src/cli/cmd/tui/routes/session/question.tsx
	content := `
  ┃   Next steps   Aspire wt config   Skill overlap   Confirm
  ┃
  ┃  Now that superpowers is installed, what would you like to do next? (select all that apply)
  ┃
  ┃  1. [ ] Configure wt for multi-repo
  ┃     Set up custom pattern on both machines
  ┃  2. [ ] Add wt hooks
  ┃     Configure post_create/post_checkout hooks
  ┃  3. [ ] Type your own answer
  ┃
  ┃  ⇆ tab  ↑↓ select  enter toggle  esc dismiss
  ┃
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if !result.Blocked {
		t.Error("expected Blocked=true")
	}
	// WaitingFor should contain tab header info
	if !strings.Contains(result.WaitingFor, "[tabs]") {
		t.Errorf("WaitingFor should contain [tabs], got: %q", result.WaitingFor)
	}
	if !strings.Contains(result.WaitingFor, "Next steps") {
		t.Errorf("WaitingFor should contain tab name, got: %q", result.WaitingFor)
	}
	// Should have Tab and BTab actions
	hasTab := false
	hasBTab := false
	for _, a := range result.Actions {
		if a.Keys == "Tab" {
			hasTab = true
		}
		if a.Keys == "BTab" {
			hasBTab = true
		}
	}
	if !hasTab {
		t.Error("expected Tab action for multi-tab navigation")
	}
	if !hasBTab {
		t.Error("expected BTab action for multi-tab navigation")
	}
	// Should still have toggle actions
	if len(result.Actions) < 3 {
		t.Errorf("expected at least 3 actions (toggles + submit + tab + dismiss), got %d", len(result.Actions))
	}
}

func TestOpenCode_ConfirmTab(t *testing.T) {
	// Confirm tab of a multi-question form: shows "Review" with answer summary,
	// no numbered options, and "⇆ tab" + "enter submit" footer.
	// Source: packages/opencode/src/cli/cmd/tui/routes/session/question.tsx
	content := `
  ┃   Next steps   Aspire wt config   Skill overlap   Confirm
  ┃
  ┃  Review
  ┃
  ┃  Next steps: Configure wt for multi-repo, Add wt hooks
  ┃  Aspire wt config: (not answered)
  ┃  Skill overlap: Authentication
  ┃
  ┃  ⇆ tab  enter submit  esc dismiss
  ┃
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if !result.Blocked {
		t.Error("expected Blocked=true")
	}
	if !strings.Contains(result.Reason, "confirm") {
		t.Errorf("reason should mention confirm, got: %q", result.Reason)
	}
	// WaitingFor should contain tab headers and review content
	if !strings.Contains(result.WaitingFor, "[confirm tab]") {
		t.Errorf("WaitingFor should contain [confirm tab], got: %q", result.WaitingFor)
	}
	if !strings.Contains(result.WaitingFor, "Configure wt") {
		t.Errorf("WaitingFor should contain review answers, got: %q", result.WaitingFor)
	}
	// Should have Enter (submit), Tab, BTab, Escape actions
	if len(result.Actions) != 4 {
		t.Errorf("expected 4 actions (Enter, Tab, BTab, Escape), got %d", len(result.Actions))
	}
	if result.Actions[0].Keys != "Enter" || result.Actions[0].Label != "submit all answers" {
		t.Errorf("first action should be Enter/submit, got: %+v", result.Actions[0])
	}
}

// --- OpenCode Subagent Task Tests ---
//
// These tests verify detection of active subagent (Task tool) execution.
// When a subagent Task is running, the TUI shows:
//   - BlockTool with braille spinner + "General Task" (or similar) title
//   - "{description} ({N} toolcalls)" below the title
//   - "└ {ToolName} {title}" for the current tool
//   - "{keybind} view subagents" text
//   - Status bar: "esc interrupt" (prompt area, not "> ")
//
// Source: packages/opencode/src/cli/cmd/tui/routes/session/index.tsx
//   Task component (line 1852), BlockTool (line 1602), Spinner (spinner.tsx:8)

func TestOpenCode_SubagentTask_ActiveWithSpinner(t *testing.T) {
	// Simulates a running subagent Task as captured by tmux capture-pane -p -J.
	// The braille spinner and "esc interrupt" should be detected as active execution.
	// SubagentInfo should be populated from the Task block content.
	content := `
  Previous conversation output...

  ⠹ General Task
  implement the feature (3 toolcalls)
  └ Bash npm test

  ctrl+j view subagents

  esc interrupt
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false: subagent Task with braille spinner is actively executing")
	}
	// SubagentInfo should be populated
	if len(result.Subagents) != 1 {
		t.Fatalf("expected 1 subagent, got %d", len(result.Subagents))
	}
	sub := result.Subagents[0]
	if sub.AgentType != "General" {
		t.Errorf("agent_type: got %q, want %q", sub.AgentType, "General")
	}
	if sub.Description != "implement the feature" {
		t.Errorf("description: got %q, want %q", sub.Description, "implement the feature")
	}
	if sub.ToolCalls != 3 {
		t.Errorf("tool_calls: got %d, want %d", sub.ToolCalls, 3)
	}
	if sub.CurrentTool != "Bash npm test" {
		t.Errorf("current_tool: got %q, want %q", sub.CurrentTool, "Bash npm test")
	}
}

func TestOpenCode_SubagentTask_EarlyPhaseZeroToolcalls(t *testing.T) {
	// Early phase of subagent dispatch: Task is running but has 0 toolcalls yet.
	// The (0 toolcalls) exclusion in isActiveExecution should NOT prevent
	// detection when other active indicators (spinner, esc interrupt) are present.
	content := `
  Previous conversation output...

  ⠋ General Task
  research the codebase (0 toolcalls)

  ctrl+j view subagents

  esc interrupt
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false: subagent Task with 0 toolcalls but spinner present is actively executing")
	}
	// SubagentInfo should be populated even with 0 toolcalls
	if len(result.Subagents) != 1 {
		t.Fatalf("expected 1 subagent, got %d", len(result.Subagents))
	}
	sub := result.Subagents[0]
	if sub.AgentType != "General" {
		t.Errorf("agent_type: got %q, want %q", sub.AgentType, "General")
	}
	if sub.Description != "research the codebase" {
		t.Errorf("description: got %q, want %q", sub.Description, "research the codebase")
	}
	if sub.ToolCalls != 0 {
		t.Errorf("tool_calls: got %d, want %d", sub.ToolCalls, 0)
	}
}

func TestOpenCode_SubagentTask_ViewSubagentsIndicator(t *testing.T) {
	// The "view subagents" text is unique to running Task blocks.
	// It should be recognized as an active execution indicator.
	content := `
  Previous conversation output...

  ⠼ Explore Task
  find all parsers (1 toolcalls)
  └ Grep pattern

  ctrl+j view subagents

  esc interrupt
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false: 'view subagents' indicates active subagent execution")
	}
	// SubagentInfo should be populated with Explore type
	if len(result.Subagents) != 1 {
		t.Fatalf("expected 1 subagent, got %d", len(result.Subagents))
	}
	sub := result.Subagents[0]
	if sub.AgentType != "Explore" {
		t.Errorf("agent_type: got %q, want %q", sub.AgentType, "Explore")
	}
	if sub.CurrentTool != "Grep pattern" {
		t.Errorf("current_tool: got %q, want %q", sub.CurrentTool, "Grep pattern")
	}
}

func TestOpenCode_SubagentTask_CompletedNotActive(t *testing.T) {
	// Completed subagent Task should NOT be detected as active.
	// The title is rendered as "# General Task" (no spinner) and the prompt
	// is "> " (idle).
	// Subagents should NOT be populated for completed tasks.
	content := `
  Previous conversation output...

  # General Task
  implement the feature (5 toolcalls)

  Some output from the task...
  More output lines here...
  And another line...
  Task completed successfully.

  >
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true: completed subagent Task should show as idle at prompt")
	}
	if len(result.Subagents) != 0 {
		t.Errorf("expected 0 subagents for completed task, got %d", len(result.Subagents))
	}
}

func TestOpenCode_SubagentTask_ZeroToolcallsNoSpinnerFallback(t *testing.T) {
	// Edge case: what if tmux capture-pane doesn't capture the braille spinner
	// but DOES capture the text? The "esc interrupt" at the bottom should
	// still be detected. This tests that "esc interrupt" alone is sufficient.
	content := `
  Previous conversation output...

  General Task
  research the codebase (0 toolcalls)

  esc interrupt
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false: 'esc interrupt' alone should indicate active execution even without spinner")
	}
}

func TestOpenCode_SubagentTask_DelegatingPending(t *testing.T) {
	// InlineTool pending state: "~ Delegating..." shown before BlockTool renders.
	// This is a transient state but should be detected as active execution
	// because it indicates a subagent dispatch is in progress.
	// The "esc interrupt" status bar will also be present.
	content := `
  Previous conversation output...

  ~ Delegating...

  esc interrupt
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Blocked {
		t.Error("expected blocked=false: 'Delegating...' with 'esc interrupt' indicates active subagent dispatch")
	}
}

func TestOpenCode_SingleQuestionNoTabs(t *testing.T) {
	// Single question without tabs — no Tab/BTab actions.
	content := `
  ┃  Which database?
  ┃
  ┃  1. PostgreSQL
  ┃  2. SQLite
  ┃
  ┃  ↑↓ select  enter submit  esc dismiss
  ┃
`
	p := &OpenCodeParser{}
	result := p.Parse(content, []string{"opencode"})
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	for _, a := range result.Actions {
		if a.Keys == "Tab" || a.Keys == "BTab" {
			t.Errorf("single question should not have Tab/BTab actions, found: %+v", a)
		}
	}
	if strings.Contains(result.WaitingFor, "[tabs]") {
		t.Errorf("single question should not have [tabs] in WaitingFor, got: %q", result.WaitingFor)
	}
}
