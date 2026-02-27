# Hook-First Assistant Dashboard Design

## Context

`pane-patrol` currently detects blocked assistants by scanning tmux panes, capturing terminal content, and applying deterministic parsers for known agents. This works for many cases but is brittle for nuanced "waiting" states and requires repeated pane capture/parsing.

We want a hook-first approach where assistants (Claude Code, OpenCode, Codex, others) emit explicit state events. The dashboard should show assistants needing attention and preserve deterministic jump-to-pane navigation.

## Goals

- Build a hook-first dashboard for assistant attention states.
- Keep deterministic navigation from dashboard row to tmux pane.
- Ensure assistant hooks are true fire-and-forget and never block user interaction.
- Keep v1 minimal and simple to reason about.
- Support isolated, no-host-impact testing.

## Non-Goals (v1)

- No pane-content fallback parsing for hook-enabled assistants.
- No delivery retries, queueing, or durable event storage.
- No cross-user event trust model.
- No non-tmux multiplexer support in initial release.
- No signature-based provenance checks.

## High-Level Architecture (Minimal v1)

1. `pane-patrol supervisor` starts an in-process local event collector.
2. Assistant hooks emit minimal JSON events to collector via Unix datagram socket.
3. Collector keeps latest in-memory state keyed by tmux target.
4. Dashboard renders attention states from event state table.
5. Enter/jump uses existing `tmux switch-client -t <target>` behavior.

There is no parser fallback for these hook-driven states in v1.

## Event Transport

### IPC choice

- Unix domain datagram socket (`SOCK_DGRAM`).
- Reason: enables non-blocking fire-and-forget semantics with immediate failure when no listener exists.

### Socket location

- Preferred: `${XDG_RUNTIME_DIR}/pane-patrol/events.sock`
- Fallback: `/tmp/pane-patrol-${UID}/events.sock`

### Fire-and-forget contract (hard requirement)

Hook adapters must:

- attempt one non-blocking send with tiny timeout budget (for example <=10ms),
- drop event on transport errors (`ENOENT`, `ECONNREFUSED`, `EAGAIN`, `ENOBUFS`),
- exit success (`0`) on send failure,
- never retry.

This guarantees hooks do not delay assistant UX when collector is down.

## Security and Permissions

Trust model for v1: same-UID local trust.

- Socket parent directory permissions: `0700`.
- Socket file permissions: `0600`.
- Collector only listens on local Unix socket (no TCP listener).
- Where OS support exists, collector verifies sender UID matches process UID.

Documented caveat: any process running as same user can spoof events in v1.

## Event Schema (Minimal v1)

Required fields:

- `assistant`: string (for example `claude`, `opencode`, `codex`)
- `state`: enum (`waiting_input`, `waiting_approval`, `running`, `completed`, `error`, `idle`)
- `target`: tmux target string (`session:window.pane`)
- `ts`: RFC3339 timestamp

Optional fields (deferred by default):

- `message`: short human-readable context
- `instance_id`: disambiguator for future multi-instance edge cases

Collector rejects malformed payloads and unknown states.

## Pane Mapping and Navigation

Navigation remains pane-target based.

- Dashboard rows are keyed by `target`.
- Jump action reuses existing `tmux switch-client -t <target>` implementation.

Hook payloads do not naturally include pane target, so adapters resolve it before send:

1. Read `TMUX_PANE` from environment.
2. Run `tmux display-message -t "$TMUX_PANE" -p "#{session_name}:#{window_index}.#{pane_index}"`.
3. Use resulting target in event.

If target cannot be resolved, adapter drops the event in v1.

## Dashboard Behavior

- Primary list shows targets in attention-required states (`waiting_input`, `waiting_approval`).
- Optional secondary view can include non-blocking states.
- State table stores latest event per target in memory.
- Stale entries expire with short TTL (for example 2-5 minutes) to remove dead panes.

## Testing Strategy (Isolation First)

All integration/E2E tests must avoid touching developer tmux sessions or hook directories.

### Isolation model

- Run tests in disposable container/devcontainer.
- Use dedicated tmux server socket name (for example `tmux -L pane-patrol-test`).
- Use container-local `HOME` so hook install targets are sandboxed.
- Use container-local runtime dir for collector socket.

### Test layers

1. Adapter unit tests
   - resolves target from mocked `TMUX_PANE`/`tmux display-message`
   - drops and exits success on unresolved mapping
   - exits success on send failures
2. Collector unit tests
   - accepts valid minimal event
   - rejects invalid state/target
   - expires stale entries by TTL
3. Navigation integration test
   - inject event with known target
   - trigger jump action
   - assert `tmux switch-client -t <target>` command invocation
4. Containerized E2E smoke test
   - start supervisor+collector in isolated tmux server
   - send synthetic hook event
   - verify blocked row appears and jump works

## Rollout Plan

1. Add minimal collector and in-memory state store behind feature flag.
2. Add one adapter (Claude or OpenCode) plus `just install-hooks` scaffold.
3. Wire dashboard to event state and keep jump flow unchanged.
4. Add isolated integration + E2E harness.
5. Expand adapters to Codex and others.

## Risks and Mitigations

- Missed events when collector down: acceptable by design; adapters emit snapshot state and next event refreshes view.
- Event spoofing by same user: acceptable in v1; clearly documented trust boundary.
- Mapping failures outside tmux: explicit drop behavior, documented setup expectations.
- Complexity creep: keep schema minimal and defer durability/signatures.

## Acceptance Criteria

- Hook events drive blocked/waiting dashboard entries without pane-content parsing fallback.
- Hooks never block assistant flows when collector is absent.
- Enter/jump from dashboard lands on correct tmux pane target.
- Tests run in isolated environment with no host tmux/hook side effects.
