# Guidelines for AI Coding Agents

## ZFC compliance

When working on this project, **never** add Go code that interprets pane
content or makes judgment calls about whether something is an agent or blocked.
All classification must go through the LLM evaluator.

Acceptable in Go code:
- Transport (capturing panes, calling APIs, formatting output)
- User-specified filtering (regex on session names)
- Error handling
- Configuration resolution

Not acceptable in Go code:
- Regex or string matching on pane content to detect agents
- Hardcoded lists of agent process names for classification
- Heuristics like "idle > N minutes = stuck"
- Any interpretation of what's on screen

See [docs/design-principles.md](docs/design-principles.md) for the full
ZFC specification.

## Building

```bash
# Build
just build

# Run tests
just test

# Build for all platforms
just build-all
```

## Testing

- Unit tests mock the `Multiplexer` and `Evaluator` interfaces.
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
  mux/                               Multiplexer abstraction (tmux, zellij)
  evaluator/                         LLM evaluation (Anthropic, OpenAI)
    prompts/                         Externalized prompt templates (embedded)
  model/                             Shared types (Verdict, Pane)
docs/                                Design documentation
```

## Prompts

LLM prompts live in `internal/evaluator/prompts/` as markdown files. They are
embedded into the binary at compile time via `//go:embed`. To iterate on prompts:

1. Edit the `.md` files directly
2. Rebuild (`just build`)
3. Test with `pane-patrol check <target>`
4. Review the `reasoning` field in the output

The `--verbose` flag includes raw pane content in the output, which is useful
for building a feedback dataset.
