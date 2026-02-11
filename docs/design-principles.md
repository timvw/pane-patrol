# Design Principles

## ZFC: Zero False Commands

pane-patrol follows the ZFC principle, inspired by [Gastown](https://github.com/steveyegge/gastown).

**Core tenet: Go transports. AI decides.**

### What Go code does

- Captures pane content from the terminal multiplexer
- Sends that content to an LLM
- Formats and outputs the LLM's verdict as JSON
- Provides transport (HTTP, tmux/zellij CLI, stdio)

### What Go code never does

- Interprets pane content
- Decides whether a pane is "blocked" or "not blocked"
- Determines whether a process is an "agent" or not
- Applies heuristics, regex, or pattern matching to classify state
- Makes threshold-based judgments (e.g., "idle > 10 min = stuck")

### What the LLM decides

The LLM makes **all** judgment calls:

- Is this an AI coding agent? Which one?
- Is this session blocked / waiting for human input?
- Why is it blocked? What kind of interaction is required?

### Acceptable exceptions

Some decisions are inherently mechanical and not judgment calls. These are
acceptable in Go code:

- **User-provided filters**: `--filter "^wt-"` is a user decision, not a
  code decision. Go applies it as instructed.
- **Multiplexer detection**: Checking `$TMUX` / `$ZELLIJ` env vars to pick
  the right backend is infrastructure plumbing, not content interpretation.
- **API routing**: Choosing the right SDK (Anthropic vs OpenAI) based on
  `--provider` is configuration, not judgment.
- **Error handling**: Detecting that a pane doesn't exist or an API call
  failed is transport-level.

## Observable reality as source of truth

State is derived from what is directly observable:

- Pane content comes from `tmux capture-pane` (or equivalent) — the actual
  text on screen.
- Process identity comes from `pane_current_command` — what the OS reports.
- Session/pane topology comes from `list-sessions` / `list-panes` — the
  multiplexer's live state.

No shadow state files, no PID tracking, no in-memory caches that can drift.

## Composable commands

Each command does one thing:

| Command   | Responsibility                                     |
|-----------|----------------------------------------------------|
| `capture` | Transport: multiplexer -> stdout                   |
| `list`    | Transport: multiplexer -> pane targets              |
| `check`   | Transport: multiplexer -> LLM -> JSON verdict      |
| `scan`    | Orchestration: list -> check (N panes) -> JSON array|
| `watch`   | Orchestration: scan on interval -> event stream     |

Higher-level commands compose lower-level ones. Each is independently useful.

## Feedback loop design

Every verdict includes `reasoning` — the LLM's step-by-step analysis. This
enables:

- **Human review**: Operators can audit whether the LLM's judgment was correct.
- **Prompt improvement**: Patterns of incorrect verdicts inform prompt refinements.
- **Training data**: Verdict + pane content pairs (via `--verbose`) can be
  collected for evaluation.

The prompts are externalized as markdown files (`internal/evaluator/prompts/`)
and embedded at compile time via `//go:embed`. This makes them easy to review,
diff, and iterate on without touching Go code.

## Provider agnosticism

The tool supports multiple LLM providers (Anthropic, OpenAI-compatible) and
multiple terminal multiplexers (tmux, zellij). Swapping either should not
change the evaluation logic or output format. The `Evaluator` and `Multiplexer`
interfaces enforce this separation.
