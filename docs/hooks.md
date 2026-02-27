# Hook Adapters

This document describes the minimal hook adapter contract used by
hook-first supervisor mode.

## Contract

Each adapter emits one JSON event with:

- `assistant`
- `state`
- `target`
- `ts`
- optional `message`

State values:

- `waiting_input`
- `waiting_approval`
- `running`
- `completed`
- `error`
- `idle`

## Target resolution

Adapters resolve tmux target at runtime:

1. read `TMUX_PANE`
2. run `tmux display-message -t "$TMUX_PANE" -p "#{session_name}:#{window_index}.#{pane_index}"`
3. include target in payload

If target cannot be resolved, adapters drop the event and exit success.

## Fire-and-forget behavior

- Unix datagram socket
- non-blocking best-effort send
- no retries
- send failure is ignored; adapter exits 0

## Install

```bash
just install-hooks
```

Default install targets:

- `~/.claude/hooks/pane-patrol-emit.sh`
- `~/.config/opencode/hooks/pane-patrol-emit.sh`
- `~/.config/codex/hooks/pane-patrol-emit.sh`

## Event socket

Supervisor defaults:

- `${XDG_RUNTIME_DIR}/pane-patrol/events.sock`
- fallback: `/tmp/pane-patrol-${UID}/events.sock`

Override with:

```bash
pane-patrol supervisor --event-socket /custom/path/events.sock
```
