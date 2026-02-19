# Subagent Detection via Process Tree

**Date:** 2026-02-19
**Status:** Approved

## Problem

When OpenCode dispatches a Task (subagent), the parent pane's TUI goes quiet:
no spinner, no active indicator. The subagent runs as a headless child process
(`opencode -s ses_<id>`) with no tmux pane of its own. pane-patrol
misclassifies the parent as "idle at prompt" when it is actually working.

## Approach

Deterministic process tree pattern matching. The parser already receives
`processTree []string` on every call. We add a check: if no active TUI
indicators are found but the process tree contains a child matching the known
subagent pattern, classify as working rather than idle/blocked.

No CPU heuristics, no log file scanning, no state tracking across scans.
Process existence is the signal -- if the child process is alive, the parent
is working; when the child exits, it disappears from the tree.

## Scope

OpenCode only. The subagent pattern (`opencode` + `-s` + `ses_`) is derived
from OpenCode's source code. Other agents can be added later when their
subagent process patterns are documented.

## Changes

### 1. Verdict struct (`internal/model/types.go`)

Add:

```go
type SubagentInfo struct {
    PID       int    `json:"pid"`
    SessionID string `json:"session_id"`
}
```

Add `Subagents []SubagentInfo` field to `Verdict` (omitempty).

### 2. Parser Result (`internal/parser/parser.go`)

Add `Subagents []model.SubagentInfo` to `Result` so parsers can pass subagent
data upstream.

### 3. OpenCode parser (`internal/parser/opencode.go`)

New function `findSubagents(processTree []string) []model.SubagentInfo`:
scan process tree entries for processes matching `opencode` + `-s` + `ses_`
pattern. Extract PID (from indentation/position) and session ID.

In `Parse()`: after checking TUI content and finding no active indicators,
check process tree for subagents. If found, return a working verdict with
reason "subagent active". Actions remain the same as the main agent (not
blocked, standard agent actions available).

### 4. Tests (`internal/parser/parser_test.go`)

- Parent idle TUI + subagent in process tree -> working (subagent active)
- Parent idle TUI + no subagent in process tree -> idle at prompt (unchanged)
- Parent active TUI + subagent in process tree -> working (TUI takes precedence)

## Design Constraints

- Deterministic: exact pattern match on known process signatures
- No heuristics: no CPU thresholds, no "idle > N = stuck" logic
- No external state: no file reads, no cross-scan state tracking
- Observable reality: process tree from `ps` is the source of truth
