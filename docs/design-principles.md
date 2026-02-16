# Design Principles

## Hybrid architecture: Deterministic parsers + LLM fallback

pane-patrol uses a two-tier evaluation system:

### Tier 1: Deterministic parsers for known agents

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

### Tier 2: LLM fallback for unknown agents

If no parser recognizes the pane content, the system falls back to LLM
evaluation. The LLM handles agents we haven't written parsers for (Cursor,
Gemini CLI, AMP, Aider, etc.) and non-agent panes.

The LLM makes judgment calls:
- Is this an AI coding agent? Which one?
- Is this session blocked / waiting for human input?
- Why is it blocked? What kind of interaction is required?

### What stays in Go code

- **Transport**: Capturing panes, calling APIs, formatting output
- **Protocol parsing**: Recognizing known agent TUI patterns
- **User-specified filtering**: Regex on session names
- **Error handling**: Detecting missing panes, API failures
- **Configuration resolution**: Provider, model, API key selection

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
| `check`   | Parser -> LLM fallback -> JSON verdict              |
| `scan`    | Orchestration: list -> check (N panes) -> JSON array|
| `watch`   | Orchestration: scan on interval -> event stream     |

Higher-level commands compose lower-level ones. Each is independently useful.

## Feedback loop design

Every verdict includes `reasoning` — either the parser's deterministic
explanation or the LLM's step-by-step analysis. This enables:

- **Human review**: Operators can audit whether the verdict was correct.
- **Prompt improvement**: Patterns of incorrect LLM verdicts inform prompt
  refinements.
- **Training data**: Verdict + pane content pairs (via `--verbose`) can be
  collected for evaluation.
- **Parser extension**: When a new agent becomes common, a deterministic
  parser can be written from the agent's source code.

The LLM prompts are externalized as markdown files
(`internal/evaluator/prompts/`) and embedded at compile time via `//go:embed`.

## Provider agnosticism

The tool supports multiple LLM providers (Anthropic, OpenAI-compatible) and
multiple terminal multiplexers (tmux, zellij). Swapping either should not
change the evaluation logic or output format. The `Evaluator` and `Multiplexer`
interfaces enforce this separation.

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

Deterministic parsers always set the correct mode. LLM-generated actions
default to literal mode for backward compatibility.
