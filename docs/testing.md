# Testing Guide

This guide explains how to test `pane-patrol`, with focus on hook-first mode.

## Quick checks

```bash
just lint
just test
just build
```

## Hook-first tests

### Hook install test

Runs in a temporary `HOME` and verifies install paths without touching your
real local hook directories.

```bash
bash scripts/install-hooks_test.sh
```

Expected:

```text
install-hooks test passed
```

### Isolated integration smoke test

Runs in Docker with private HOME/runtime/tmux state.

```bash
bash test/integration/run.sh
```

Expected:

```text
integration hook smoke passed
```

## Manual local check

```bash
just build
just install-hooks
./bin/pane-patrol supervisor --hook-first
```

From a tmux pane, emit a synthetic event:

```bash
TMUX_PANE="$TMUX_PANE" hooks/claude/emit.sh waiting_input "manual test"
```

In the supervisor UI, confirm:

- the pane appears as waiting
- `Enter` jumps to the pane

## Fire-and-forget behavior

Stop supervisor and run:

```bash
hooks/claude/emit.sh waiting_input "no listener"
```

It should return immediately (best-effort drop when no collector exists).

## Related docs

- `docs/hooks.md`
- `docs/design-principles.md`
