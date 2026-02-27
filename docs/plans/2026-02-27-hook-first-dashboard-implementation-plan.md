# Hook-First Assistant Dashboard Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace pane-content-based blocked detection in supervisor mode with a minimal hook-first event pipeline that preserves jump-to-pane navigation.

**Architecture:** Add an in-process Unix datagram event collector to `supervisor`, normalize minimal hook events (`assistant`, `state`, `target`, `ts`), and render dashboard state from latest event per target with TTL expiry. Keep existing tmux jump behavior (`switch-client -t target`) unchanged and enforce non-blocking hook behavior by design.

**Tech Stack:** Go (std lib net/unix, encoding/json, time), Cobra CLI, Bubble Tea TUI, tmux, Docker Compose for isolated integration testing.

---

### Task 1: Add event model and validation

**Files:**
- Create: `internal/events/types.go`
- Create: `internal/events/types_test.go`

**Step 1: Write the failing test**

```go
func TestValidate_MinimalValidEvent(t *testing.T) {
    e := Event{Assistant: "claude", State: "waiting_input", Target: "s:0.1", TS: time.Now().UTC()}
    if err := e.Validate(); err != nil {
        t.Fatalf("expected valid event, got %v", err)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/events -run TestValidate_MinimalValidEvent -v`
Expected: FAIL (package/file/type missing)

**Step 3: Write minimal implementation**

```go
type Event struct {
    Assistant string    `json:"assistant"`
    State     string    `json:"state"`
    Target    string    `json:"target"`
    TS        time.Time `json:"ts"`
    Message   string    `json:"message,omitempty"`
}

func (e Event) Validate() error { /* required field + state enum + target format checks */ }
```

**Step 4: Add edge-case tests**

- missing `assistant`
- invalid `state`
- invalid `target` (not `session:window.pane`)
- zero timestamp

**Step 5: Run tests to verify they pass**

Run: `go test ./internal/events -v`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/events/types.go internal/events/types_test.go
git commit -m "feat: add hook event schema and validation"
```

### Task 2: Build in-memory state store with TTL expiry

**Files:**
- Create: `internal/events/store.go`
- Create: `internal/events/store_test.go`

**Step 1: Write failing tests**

- upsert latest event by `target`
- overwrite prior state for same `target`
- list attention states (`waiting_input`, `waiting_approval`)
- expire stale entries after TTL

**Step 2: Run tests to verify failure**

Run: `go test ./internal/events -run TestStore -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
type Store struct { /* map[target]Event + mutex + ttl */ }
func (s *Store) Upsert(e Event)
func (s *Store) Snapshot(now time.Time) []Event
```

**Step 4: Run tests to verify pass**

Run: `go test ./internal/events -run TestStore -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/events/store.go internal/events/store_test.go
git commit -m "feat: add in-memory hook event store with ttl"
```

### Task 3: Implement Unix datagram collector

**Files:**
- Create: `internal/events/collector.go`
- Create: `internal/events/collector_test.go`

**Step 1: Write failing tests**

- collector starts and binds socket path
- valid JSON event is accepted and stored
- malformed event is ignored
- oversized payload is rejected

**Step 2: Run tests to verify failure**

Run: `go test ./internal/events -run TestCollector -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
type Collector struct { /* conn, store, logger */ }
func (c *Collector) Start(ctx context.Context) error
func (c *Collector) SocketPath() string
```

Implementation requirements:
- Unix datagram socket under runtime dir
- create parent dir `0700`
- set socket `0600`
- read loop decodes JSON and calls `store.Upsert`

**Step 4: Add non-blocking sender helper test fixture**

Create helper in tests to send one datagram and assert write duration is bounded.

**Step 5: Run tests to verify pass**

Run: `go test ./internal/events -run TestCollector -v`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/events/collector.go internal/events/collector_test.go
git commit -m "feat: add local unix datagram hook collector"
```

### Task 4: Wire collector into supervisor command

**Files:**
- Modify: `cmd/supervisor.go`
- Modify: `internal/supervisor/scanner.go`

**Step 1: Write failing integration-style test**

Add/extend scanner or supervisor tests to assert event-driven mode can produce verdict rows without capture-pane parser dependency.

**Step 2: Run targeted tests to verify failure**

Run: `go test ./internal/supervisor -run TestEventDriven -v`
Expected: FAIL

**Step 3: Implement minimal wiring**

- Start collector in `runSupervisor`.
- Add event store dependency to TUI model.
- Keep scanner available behind feature flag during migration.

**Step 4: Run targeted tests**

Run: `go test ./internal/supervisor -run TestEventDriven -v`
Expected: PASS

**Step 5: Commit**

```bash
git add cmd/supervisor.go internal/supervisor/scanner.go
git commit -m "feat: wire hook collector into supervisor runtime"
```

### Task 5: Render hook states in TUI and keep jump-to-pane

**Files:**
- Modify: `internal/supervisor/tui.go`
- Modify: `internal/supervisor/tui_test.go`
- Modify: `internal/model/types.go`

**Step 1: Write failing tests first**

- row appears for event target in `waiting_input`
- row hidden/filtered when state is `running`
- Enter on row triggers existing jump path using `target`

**Step 2: Run tests to verify failure**

Run: `go test ./internal/supervisor -run TestHook -v`
Expected: FAIL

**Step 3: Implement minimal UI mapping**

- map event state -> `model.Verdict`-compatible display structure
- mark `Blocked=true` for attention states
- preserve existing `jumpToPane(target)` flow

**Step 4: Run full supervisor tests**

Run: `go test ./internal/supervisor -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/supervisor/tui.go internal/supervisor/tui_test.go internal/model/types.go
git commit -m "feat: render hook-driven attention states in supervisor tui"
```

### Task 6: Add hook adapters and install command

**Files:**
- Create: `hooks/claude/emit.sh`
- Create: `hooks/opencode/emit.sh`
- Create: `hooks/codex/emit.sh`
- Modify: `justfile`
- Create: `scripts/install-hooks.sh`
- Create: `scripts/install-hooks_test.sh`

**Step 1: Write failing shell tests**

Use a temp HOME and assert install script copies adapters into expected tool paths.

**Step 2: Run shell tests to verify failure**

Run: `bash scripts/install-hooks_test.sh`
Expected: FAIL

**Step 3: Implement adapters (minimal contract)**

- resolve target from `TMUX_PANE` + `tmux display-message`
- build minimal JSON payload
- single best-effort datagram send
- always exit 0 on send/mapping failure

**Step 4: Implement install recipe**

Add `just install-hooks` to call `scripts/install-hooks.sh`.

**Step 5: Run tests to verify pass**

Run: `bash scripts/install-hooks_test.sh`
Expected: PASS

**Step 6: Commit**

```bash
git add hooks scripts justfile
git commit -m "feat: add assistant hook adapters and install-hooks task"
```

### Task 7: Add isolated Docker-based integration harness

**Files:**
- Create: `test/integration/docker-compose.yml`
- Create: `test/integration/Dockerfile`
- Create: `test/integration/run.sh`
- Create: `test/integration/e2e_hooks_test.sh`

**Step 1: Write failing E2E script**

Script should:
- start isolated container
- launch tmux server with `-L pane-patrol-test`
- start supervisor
- emit synthetic hook event
- assert row appears and jump command is attempted

**Step 2: Run harness to verify failure**

Run: `bash test/integration/run.sh`
Expected: FAIL

**Step 3: Implement harness**

- private HOME inside container
- private runtime dir + collector socket
- no bind mounts to host tmux socket

**Step 4: Run harness to verify pass**

Run: `bash test/integration/run.sh`
Expected: PASS

**Step 5: Commit**

```bash
git add test/integration
git commit -m "test: add isolated docker integration harness for hooks"
```

### Task 8: Documentation and migration notes

**Files:**
- Modify: `README.md`
- Modify: `docs/design-principles.md`
- Create: `docs/hooks.md`

**Step 1: Write doc checklist as failing verification**

Create checklist in PR notes and verify docs mention:
- same-UID trust model
- fire-and-forget semantics
- install-hooks usage
- isolated test harness command

**Step 2: Update docs**

Add concise examples:
- `just install-hooks`
- running supervisor in hook-first mode
- running isolated integration harness

**Step 3: Validate docs and full suite**

Run:
- `just test`
- `just build`
- `bash test/integration/run.sh`

Expected: PASS

**Step 4: Commit**

```bash
git add README.md docs/design-principles.md docs/hooks.md
git commit -m "docs: describe hook-first mode trust model and testing"
```

### Task 9: Final verification and cleanup

**Files:**
- Modify: none required

**Step 1: Run full verification (required)**

Run:
- `just lint`
- `just test`
- `just build`
- `bash test/integration/run.sh`

Expected: all PASS

**Step 2: Capture evidence for PR**

- command outputs
- hook install paths used in tests
- note that host tmux and host hook dirs were untouched

**Step 3: Commit any final non-functional fixes**

```bash
git add -A
git commit -m "chore: finalize hook-first dashboard implementation"
```

## Notes for Execution

- Use `@superpowers:test-driven-development` discipline for every task.
- Use `@superpowers:verification-before-completion` before claiming success.
- Keep each commit small and task-scoped.
- Do not add pane-capture fallback behavior unless explicitly requested.
