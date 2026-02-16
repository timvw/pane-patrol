# Guidelines for AI Coding Agents

## Architecture: Deterministic parsers + LLM fallback

This project uses a hybrid evaluation approach:

1. **Deterministic parsers** (`internal/parser/`) handle known agents
   (OpenCode, Claude Code, Codex) by matching exact TUI patterns derived
   from their source code. This is protocol parsing, not heuristic
   classification.

2. **LLM fallback** (`internal/evaluator/`) handles unknown agents and
   non-agent panes.

Acceptable in Go code:
- Transport (capturing panes, calling APIs, formatting output)
- Deterministic parsing of known agent TUI patterns (exact string matches)
- User-specified filtering (regex on session names)
- Error handling and configuration resolution

Not acceptable in Go code:
- Heuristics like "idle > N minutes = stuck"
- Fuzzy or probabilistic classification
- Scoring, ranking, or quality judgment on pane content

When adding a new agent parser, derive the patterns from the agent's source
code and document the source file + line numbers in the parser's doc comment.

See [docs/design-principles.md](docs/design-principles.md) for the full
architecture specification.

## Building and quality checks

```bash
# Build
just build

# Run tests
just test

# Run linter
just lint

# Build for all platforms
just build-all
```

**Before committing**, always run lint, test, and build:

```bash
just lint && just test && just build
```

A pre-commit hook (`.pre-commit-config.yaml`) enforces this automatically.
Install it with:

```bash
pre-commit install
```

## Testing

- Unit tests mock the `Multiplexer` and `Evaluator` interfaces.
- Parser tests use inline terminal content strings.
- Integration tests require tmux and an LLM API key.
- Run `go test ./...` for unit tests.

## Branch naming

- `feat/description` — New features
- `fix/description` — Bug fixes
- `docs/description` — Documentation
- `refactor/description` — Refactoring

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/):
- `feat: add watch command`
- `fix: handle empty pane content`
- `docs: update design principles`

## Project structure

```
main.go                              Entry point
cmd/                                 Cobra commands
internal/
  parser/                            Deterministic agent parsers
    parser.go                        AgentParser interface + Registry
    opencode.go                      OpenCode TUI parser
    claude.go                        Claude Code TUI parser
    codex.go                         Codex CLI TUI parser
    parser_test.go                   Parser tests
  mux/                               Multiplexer abstraction (tmux, zellij)
  evaluator/                         LLM evaluation (Anthropic, OpenAI)
    prompts/                         Externalized prompt templates (embedded)
  model/                             Shared types (Verdict, Pane, Action)
  supervisor/                        Scan loop, TUI, nudge transport
docs/                                Design documentation
```

## Adding a new agent parser

1. Read the agent's source code to find exact TUI strings
2. Create `internal/parser/<agent>.go` implementing `AgentParser`
3. Add it to `NewRegistry()` in `parser.go`
4. Add tests in `parser_test.go` with realistic terminal content
5. Document source references (file paths + line numbers) in the doc comment
6. Run `just test` and `just build`

## Prompts

LLM prompts live in `internal/evaluator/prompts/` as markdown files. They are
embedded into the binary at compile time via `//go:embed`. They serve as the
fallback for agents not covered by deterministic parsers.

The `--verbose` flag includes raw pane content in the output, which is useful
for building a feedback dataset.
