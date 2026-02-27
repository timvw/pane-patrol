# Design Principles

## Hook-first architecture

pane-patrol supervisor mode is hook-first: assistant hooks emit explicit state
events, and the dashboard renders those states directly.

### Hook events as primary source of truth

For supported assistants, supervisor state comes from hook events, not from
inferring status out of pane scrollback.

Benefits:
- **Deterministic state**: explicit `waiting_input` / `waiting_approval`
- **Deterministic routing**: event includes tmux target for exact jump
- **No inference drift**: no fragile pattern guessing in supervisor mode

### Event transport model

- Local Unix datagram socket (same host, same user boundary)
- Fire-and-forget delivery (no hook blocking when collector is down)
- In-memory latest-state map keyed by tmux target

## Deterministic parser architecture

Deterministic parsers still power direct pane-inspection commands and non-hook
paths. Unknown panes are classified as "not_an_agent".

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

State is derived from directly observable sources:

- Hook event state comes from assistant runtime hooks.
- Pane content comes from `tmux capture-pane` for parser-based paths.
- Process identity comes from `pane_current_command` — what the OS reports.
- Session/pane topology comes from `list-sessions` / `list-panes` — the
  multiplexer's live state.

No shadow state files and no caches that can drift from reality.

**Note on cache scope:** content-hash verdict caching applies to parser-based
pane scanning. Hook-first supervisor mode uses latest hook event state per
target instead of capture-pane verdict caching.

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

Supervisor uses hook-first mode where assistant hook events are the source of
truth for blocked/waiting states.

Filter semantics in supervisor:

- `blocked`: only blocked assistant panes
- `agents`: all assistant panes (blocked and non-blocked)
- `all`: all panes

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
