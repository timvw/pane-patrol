# pane-patrol

ZFC-compliant terminal pane monitor — AI observes AI coding agents for blocked/waiting states.

## What it does

pane-patrol monitors terminal multiplexer panes (tmux, zellij) and uses an LLM
to determine if AI coding agents are blocked waiting for human input —
confirmation dialogs, permission prompts, questions, or interactive selections.

Following [ZFC (Zero False Commands)](docs/design-principles.md) principles,
**all judgment calls are made by the LLM**. Go code only provides transport.

## Installation

### Homebrew (macOS / Linux)

```bash
brew install timvw/tap/pane-patrol
```

### Go install

```bash
go install github.com/timvw/pane-patrol@latest
```

### From source

```bash
git clone https://github.com/timvw/pane-patrol.git
cd pane-patrol
go build -o bin/pane-patrol .
```

## Configuration

pane-patrol uses environment variables for LLM configuration:

### Azure AI Foundry (Anthropic models)

```bash
export AZURE_RESOURCE_NAME="your-resource"
export AZURE_OPENAI_API_KEY="your-key"
```

### Direct Anthropic API

```bash
export ANTHROPIC_API_KEY="your-key"
```

### Direct OpenAI API

```bash
export OPENAI_API_KEY="your-key"
pane-patrol scan --provider openai
```

### Override with flags

```bash
pane-patrol check session:0.0 \
  --provider anthropic \
  --model claude-sonnet-4-5 \
  --base-url https://custom-endpoint.example.com/v1 \
  --api-key sk-...
```

## Usage

### List all panes

```bash
pane-patrol list
pane-patrol list --filter "^wt-"
```

### Capture pane content

```bash
pane-patrol capture mysession:0.0
```

### Check a single pane

```bash
pane-patrol check mysession:0.0
```

Output:
```json
{
  "target": "mysession:0.0",
  "session": "mysession",
  "window": 0,
  "pane": 0,
  "command": "node",
  "agent": "claude-code",
  "blocked": true,
  "reason": "Permission dialog waiting for user approval",
  "reasoning": "The screen shows Claude Code's TUI with a 'Confirm/Cancel' dialog...",
  "model": "claude-sonnet-4-5",
  "provider": "anthropic",
  "evaluated_at": "2026-02-11T14:32:01Z"
}
```

### Scan all panes

```bash
# Scan all panes sequentially
pane-patrol scan

# Scan with filter and parallelism
pane-patrol scan --filter "^wt-" --parallel 4

# Include raw pane content in output
pane-patrol scan --verbose
```

### Find blocked panes

```bash
# Pipe through jq to find only blocked panes
pane-patrol scan | jq '[.[] | select(.blocked == true)]'

# Find blocked agents specifically
pane-patrol scan | jq '[.[] | select(.blocked == true and .agent != "not_an_agent")]'
```

## Global flags

| Flag           | Env var                | Default             | Description                          |
|----------------|------------------------|---------------------|--------------------------------------|
| `--mux`        | `PANE_PATROL_MUX`      | auto-detect         | Terminal multiplexer (tmux, zellij)  |
| `--provider`   | `PANE_PATROL_PROVIDER` | `anthropic`         | LLM provider (anthropic, openai)     |
| `--model`      | `PANE_PATROL_MODEL`    | `claude-sonnet-4-5` | LLM model name                       |
| `--base-url`   | `PANE_PATROL_BASE_URL` | (from Azure)        | Override LLM API endpoint            |
| `--api-key`    | `PANE_PATROL_API_KEY`  | (from Azure)        | Override LLM API key                 |
| `--max-tokens` | —                      | `4096`              | Max completion tokens for LLM        |
| `--verbose`    | —                      | `false`             | Include raw pane content in output   |

## Supported models

pane-patrol works with any model accessible via the Anthropic Messages API or OpenAI Chat Completions API. Below is the tested status on Azure AI Foundry:

### Anthropic (provider: `anthropic`)

| Model | Status | Notes |
|-------|--------|-------|
| `claude-sonnet-4-5` | Supported (default) | Recommended — good balance of speed and accuracy |
| `claude-opus-4-5` | Supported | Higher quality, slower and more expensive |
| `claude-opus-4-6` | Supported | |
| `claude-haiku-4-5` | Not working | Hangs at HTTP level (Azure infrastructure issue) |

### OpenAI (provider: `openai`)

| Model | Status | Notes |
|-------|--------|-------|
| `gpt-4o-mini` | Supported (default for openai) | Fast and cheap |
| `gpt4o` | Supported | Note: Azure deployment name has no hyphen |
| `gpt-5` | Supported | Reasoning model — uses `--max-tokens` budget for chain-of-thought |
| `gpt-5.1` | Supported | Reasoning model — uses `--max-tokens` budget for chain-of-thought |
| `gpt-5.x-codex` | Not supported | Uses Responses API (`/responses`), not Chat Completions |

> **Reasoning models (gpt-5, gpt-5.1):** These models allocate part of the
> `--max-tokens` budget to internal chain-of-thought reasoning. The default of
> 4096 tokens provides enough headroom. If you see empty responses, increase
> `--max-tokens`.

## Design

See [docs/design-principles.md](docs/design-principles.md) for the full design
philosophy, including ZFC compliance, composability, and feedback loop design.

## License

MIT
