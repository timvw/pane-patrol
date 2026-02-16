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

func TestClaude_CompletedNotActive(t *testing.T) {
	// "✻ Worked for" is the COMPLETED state — not active.
	content := `
  Task completed successfully.

✻ Worked for 3m 10s

❯
? for shortcuts
`
	p := &ClaudeCodeParser{}
	result := p.Parse(content, []string{"claude"})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Blocked {
		t.Error("expected blocked=true after completion (✻ Worked for = idle)")
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
	// Stale "▣ Build" from a previous turn visible above an idle prompt.
	content := `
  ▣ Build · claude-sonnet-4-5 · 45s

  ■■■■■■■■

  Build completed. Here are the results...

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
