# Design Principles

## Deterministic parser architecture

pane-patrol uses deterministic parsers for known agents. Unknown panes are
classified as "not_an_agent".

### Deterministic parsers for known agents

For agents whose TUI is known (OpenCode, Claude Code, Codex), Go code parses
the terminal output directly. This is **protocol parsing**, not heuristic
classification — we know exactly what strings these agents render because we
read their source code.

Benefits:
- **Instant**: No API call, no latency
- **Free**: No token costs
- **100% accurate**: Exact string matching against known UI patterns
- **Correct actions**: Produces the right keystrokes with proper send mode
  (raw for TUIs in raw mode, literal for readline-based shells)

The parsers live in `internal/parser/` with one implementation per agent:
- `opencode.go` — Permission dialogs, Build/Plan indicators, spinner detection
- `claude.go` — Permission dialogs, edit approvals, auto-resolve countdowns
- `codex.go` — Exec/edit/network/MCP approvals, Working indicator

### What stays in Go code

- **Transport**: Capturing panes, calling APIs, formatting output
- **Protocol parsing**: Recognizing known agent TUI patterns
- **User-specified filtering**: Regex on session names
- **Error handling**: Detecting missing panes, API failures
- **Configuration resolution**: Refresh interval, cache TTL, exclusion lists

## Observable reality as source of truth

State is derived from what is directly observable:

- Pane content comes from `tmux capture-pane` (or equivalent) — the actual
  text on screen.
- Process identity comes from `pane_current_command` — what the OS reports.
- Session/pane topology comes from `list-sessions` / `list-panes` — the
  multiplexer's live state.

No shadow state files, no PID tracking, no caches that can drift from reality.

**Note on the verdict cache:** The supervisor uses a content-hash cache
(SHA256 of pane content) with TTL expiry and active invalidation on
nudge actions. This is compatible with the observable-reality principle
because the cache key IS the observed content — if any pixel changes
(a spinner frame, a new log line), the hash changes and the cache misses.
The TTL ensures periodic re-evaluation even when content is static.

## Composable commands

Each command does one thing:

| Command   | Responsibility                                     |
|-----------|-----------------------------------------------------|
| `capture` | Transport: multiplexer -> stdout                    |
| `list`    | Transport: multiplexer -> pane targets              |
| `check`   | Parser -> JSON verdict                               |
| `scan`    | Orchestration: list -> check (N panes) -> JSON array|
| `watch`   | Orchestration: scan on interval -> event stream     |

Higher-level commands compose lower-level ones. Each is independently useful.

## Feedback loop design

Every verdict includes `reasoning` — the parser's deterministic explanation.
This enables:

- **Human review**: Operators can audit whether the verdict was correct.
- **Training data**: Verdict + pane content pairs (via `--verbose`) can be
  collected for evaluation.
- **Parser extension**: When a new agent becomes common, a deterministic
  parser can be written from the agent's source code.

## Multiplexer agnosticism

The tool supports multiple terminal multiplexers (tmux, zellij). Swapping
the multiplexer should not change the evaluation logic or output format.
The `Multiplexer` interface enforces this separation.

## Hook-first dashboard mode

Supervisor can run in hook-first mode where assistant hook events are the
source of truth for blocked/waiting states.

- Events are delivered over local Unix datagram socket.
- State is normalized to deterministic enums (`waiting_input`, `waiting_approval`, etc.).
- Dashboard keeps latest state per tmux target and uses existing target-based jump.
- No pane-content fallback parsing in hook-first mode.

### Trust boundary

v1 uses same-UID trust:

- socket directory `0700`
- socket file `0600`
- local-only Unix socket (no network listener)

Any process running as same user can emit events; this is documented and
accepted for v1 simplicity.

## Nudge modes

Actions include a `raw` flag that controls how keystrokes are sent:

- **Raw mode** (`raw: true`): Single keypress, no Escape or Enter appended.
  Used for TUIs that run in raw mode (Claude Code, OpenCode, Codex) where
  each keypress is processed immediately.
- **Literal mode** (`raw: false`, default): The Gastown pattern — literal
  text with `-l` flag, then Escape (exit vim INSERT if applicable), then
  Enter with retry. Used for readline-based shells and text inputs.
- **Multi-key sequences** (e.g., `"Down Enter"`): Space-separated control
  sequences are sent as individual raw keystrokes with 100ms delays.

Deterministic parsers always set the correct mode.
