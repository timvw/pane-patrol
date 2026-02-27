package supervisor

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/timvw/pane-patrol/internal/model"
)

// newTestModel creates a tuiModel with a single blocked verdict, cursor on
// the pane item. Suitable for testing list navigation and keyboard handling.
func newTestModel(v model.Verdict) *tuiModel {
	m := &tuiModel{
		verdicts:        []model.Verdict{v},
		expanded:        map[string]bool{v.Session: true},
		manualCollapsed: make(map[string]bool),
		width:           120,
		height:          40,
	}
	m.rebuildGroups()
	// Move cursor to the pane item (skip session header)
	for i, item := range m.items {
		if item.kind == itemPane {
			m.cursor = i
			break
		}
	}
	return m
}

// simpleVerdict returns a blocked pane verdict for testing.
func simpleVerdict() model.Verdict {
	return model.Verdict{
		Target:  "test:0.0",
		Session: "test",
		Blocked: true,
		Agent:   "opencode",
		Reason:  "permission dialog waiting for approval",
		Actions: []model.Action{
			{Keys: "Enter", Label: "allow once", Risk: "medium", Raw: true},
			{Keys: "Escape", Label: "dismiss", Risk: "low", Raw: true},
		},
	}
}

// --- List panel: keyboard navigation ---

func TestListKey_EnterOnPane_JumpsToTmuxPane(t *testing.T) {
	m := newTestModel(simpleVerdict())
	msg := tea.KeyMsg{Type: tea.KeyEnter}
	_, _ = m.handleVerdictListKey(msg)

	// Enter jumps to the tmux pane (switch-client).
	// jumpToPane will fail without tmux, but the handler should execute.
	// No state change expected other than possible error message.
}

func TestListKey_EnterOnSession_TogglesExpand(t *testing.T) {
	m := newTestModel(simpleVerdict())
	// Move cursor to session header (index 0)
	m.cursor = 0
	wasExpanded := m.expanded[m.items[0].session]

	msg := tea.KeyMsg{Type: tea.KeyEnter}
	_, _ = m.handleVerdictListKey(msg)

	if m.expanded[m.items[0].session] == wasExpanded {
		t.Error("expected session expand state to toggle")
	}
}

func TestListKey_RightOnSession_Expands(t *testing.T) {
	m := newTestModel(simpleVerdict())
	// Collapse the session first, then press right to expand
	m.expanded["test"] = false
	m.rebuildItems()
	m.cursor = 0 // session header

	msg := tea.KeyMsg{Type: tea.KeyRight}
	_, _ = m.handleVerdictListKey(msg)

	if !m.expanded["test"] {
		t.Error("expected session to be expanded after right key")
	}
	// Cursor should move to the first pane
	if m.cursor == 0 {
		t.Error("expected cursor to advance past session header")
	}
}

func TestListKey_LeftOnPane_JumpsToSessionHeader(t *testing.T) {
	m := newTestModel(simpleVerdict())
	// Cursor should be on pane item (index 1)
	if m.items[m.cursor].kind != itemPane {
		t.Fatal("setup: expected cursor on pane item")
	}

	msg := tea.KeyMsg{Type: tea.KeyLeft}
	_, _ = m.handleVerdictListKey(msg)

	if m.items[m.cursor].kind != itemSession {
		t.Error("expected cursor to jump to session header after left key on pane")
	}
}

func TestListKey_LeftOnSession_Collapses(t *testing.T) {
	m := newTestModel(simpleVerdict())
	m.cursor = 0 // session header (already expanded)

	msg := tea.KeyMsg{Type: tea.KeyLeft}
	_, _ = m.handleVerdictListKey(msg)

	if m.expanded["test"] {
		t.Error("expected session to be collapsed after left key on session header")
	}
}

func TestListKey_UpDownNavigation(t *testing.T) {
	// Two sessions, each with one pane
	m := &tuiModel{
		verdicts: []model.Verdict{
			{Target: "a:0.0", Session: "a", Agent: "opencode", Blocked: true,
				Actions: []model.Action{{Keys: "1", Label: "opt"}}},
			{Target: "b:0.0", Session: "b", Agent: "opencode", Blocked: true,
				Actions: []model.Action{{Keys: "1", Label: "opt"}}},
		},
		expanded:        map[string]bool{"a": true, "b": true},
		manualCollapsed: make(map[string]bool),
		width:           120,
		height:          40,
	}
	m.rebuildGroups()
	// Items: [session-a, pane-a, session-b, pane-b]
	// Start on pane-a (index 1)
	m.cursor = 1

	// Down should skip session-b header and land on pane-b
	msg := tea.KeyMsg{Type: tea.KeyDown}
	_, _ = m.handleVerdictListKey(msg)

	if m.cursor < 2 {
		t.Errorf("expected cursor to skip session header, got cursor=%d", m.cursor)
	}
	item := m.items[m.cursor]
	if item.kind != itemPane {
		t.Errorf("expected cursor on pane after down, got kind=%v", item.kind)
	}
}

func TestListKey_FilterCycles(t *testing.T) {
	m := newTestModel(simpleVerdict())
	if m.filter != filterBlocked {
		t.Fatalf("expected initial filter=blocked, got %v", m.filter)
	}

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}}
	_, _ = m.handleVerdictListKey(msg)
	if m.filter != filterAgents {
		t.Errorf("expected filter=agents after first f, got %v", m.filter)
	}

	_, _ = m.handleVerdictListKey(msg)
	if m.filter != filterAll {
		t.Errorf("expected filter=all after second f, got %v", m.filter)
	}

	_, _ = m.handleVerdictListKey(msg)
	if m.filter != filterBlocked {
		t.Errorf("expected filter=blocked after third f, got %v", m.filter)
	}
}

func TestListKey_RescanTriggered(t *testing.T) {
	m := newTestModel(simpleVerdict())
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	_, _ = m.handleVerdictListKey(msg)

	if !m.scanning {
		t.Error("expected scanning=true after r key")
	}
}

func TestListKey_ToggleAutoNudge(t *testing.T) {
	m := newTestModel(simpleVerdict())
	m.autoNudge = false

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
	_, _ = m.handleVerdictListKey(msg)

	if !m.autoNudge {
		t.Error("expected autoNudge=true after a key")
	}

	_, _ = m.handleVerdictListKey(msg)
	if m.autoNudge {
		t.Error("expected autoNudge=false after second a key")
	}
}

// --- Mouse handling ---

func TestMouse_ClickOnPaneJumps(t *testing.T) {
	m := newTestModel(simpleVerdict())
	m.listStart = 0
	// Pane is at item index 1, rendered at Y=2 (header=0, session=1, pane=2... no, Y=1+index)
	// With header at Y=0 and items starting at Y=1: session at Y=1, pane at Y=2
	msg := tea.MouseMsg{X: 5, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	_, _ = m.handleMouse(msg)

	if m.cursor != 1 {
		t.Errorf("expected cursor=1 (pane item), got %d", m.cursor)
	}
}

func TestMouse_HoverMovesCursor(t *testing.T) {
	m := &tuiModel{
		verdicts: []model.Verdict{
			{Target: "a:0.0", Session: "a", Agent: "opencode", Blocked: true,
				Actions: []model.Action{{Keys: "1", Label: "opt"}}},
			{Target: "b:0.0", Session: "b", Agent: "opencode", Blocked: true,
				Actions: []model.Action{{Keys: "1", Label: "opt"}}},
		},
		expanded:        map[string]bool{"a": true, "b": true},
		manualCollapsed: make(map[string]bool),
		width:           120,
		height:          40,
	}
	m.rebuildGroups()
	m.listStart = 0
	m.cursor = 0

	// Hover over pane-b (items: [sess-a=0, pane-a=1, sess-b=2, pane-b=3], Y = 1+index)
	msg := tea.MouseMsg{X: 5, Y: 4, Action: tea.MouseActionMotion}
	_, _ = m.handleMouse(msg)

	if m.cursor != 3 {
		t.Errorf("expected cursor=3 (pane-b), got %d", m.cursor)
	}
}

// --- Cursor stability across scan rebuilds ---

func TestCursorStability_ScanRebuildPreservesSelection(t *testing.T) {
	// Simulate the bug: user selects pane B, a new pane appears in session A
	// after rescan, cursor should stay on pane B (not jump to a different item).
	m := &tuiModel{
		verdicts: []model.Verdict{
			{Target: "sessA:1.0", Session: "sessA", Agent: "opencode", Blocked: true,
				Actions: []model.Action{{Keys: "1", Label: "option 1"}}},
			{Target: "sessB:1.0", Session: "sessB", Agent: "opencode", Blocked: true,
				Actions: []model.Action{{Keys: "1", Label: "option 1"}}},
		},
		expanded:        map[string]bool{"sessA": true, "sessB": true},
		manualCollapsed: make(map[string]bool),
		width:           120,
		height:          40,
	}
	m.rebuildGroups()

	// Find and select pane B
	for i, item := range m.items {
		if item.kind == itemPane && m.verdicts[item.paneIdx].Target == "sessB:1.0" {
			m.cursor = i
			break
		}
	}
	if m.selectedVerdict().Target != "sessB:1.0" {
		t.Fatalf("setup: expected sessB:1.0 selected, got %s", m.selectedVerdict().Target)
	}

	// Simulate scan result with a new pane inserted in session A.
	// This shifts all items after it by 1 position.
	prevKey := m.selectedItemKey()
	m.verdicts = []model.Verdict{
		{Target: "sessA:1.0", Session: "sessA", Agent: "opencode", Blocked: true,
			Actions: []model.Action{{Keys: "1", Label: "option 1"}}},
		{Target: "sessA:2.0", Session: "sessA", Agent: "opencode", Blocked: true,
			Actions: []model.Action{{Keys: "1", Label: "option 1"}}},
		{Target: "sessB:1.0", Session: "sessB", Agent: "opencode", Blocked: true,
			Actions: []model.Action{{Keys: "1", Label: "option 1"}}},
	}
	m.rebuildGroups()
	m.restoreCursorByKey(prevKey)

	got := m.selectedVerdict()
	if got == nil || got.Target != "sessB:1.0" {
		target := "<nil>"
		if got != nil {
			target = got.Target
		}
		t.Errorf("cursor jumped: expected sessB:1.0, got %s (cursor=%d)", target, m.cursor)
	}
}

func TestCursorStability_PaneDisappears(t *testing.T) {
	// If the selected pane disappears, cursor should land on a valid pane
	// (not crash or point to a session header).
	m := &tuiModel{
		verdicts: []model.Verdict{
			{Target: "sessA:1.0", Session: "sessA", Agent: "opencode", Blocked: true,
				Actions: []model.Action{{Keys: "1", Label: "option 1"}}},
			{Target: "sessB:1.0", Session: "sessB", Agent: "opencode", Blocked: true,
				Actions: []model.Action{{Keys: "1", Label: "option 1"}}},
		},
		expanded:        map[string]bool{"sessA": true, "sessB": true},
		manualCollapsed: make(map[string]bool),
		width:           120,
		height:          40,
	}
	m.rebuildGroups()

	// Select pane B
	for i, item := range m.items {
		if item.kind == itemPane && m.verdicts[item.paneIdx].Target == "sessB:1.0" {
			m.cursor = i
			break
		}
	}

	// Simulate scan where pane B disappeared
	prevKey := m.selectedItemKey()
	m.verdicts = []model.Verdict{
		{Target: "sessA:1.0", Session: "sessA", Agent: "opencode", Blocked: true,
			Actions: []model.Action{{Keys: "1", Label: "option 1"}}},
	}
	m.rebuildGroups()
	m.restoreCursorByKey(prevKey)

	got := m.selectedVerdict()
	if got == nil {
		t.Fatal("cursor should point to a valid verdict after pane disappears")
	}
	if got.Target != "sessA:1.0" {
		t.Errorf("expected fallback to sessA:1.0, got %s", got.Target)
	}
	// Cursor should not be on a session header
	if m.items[m.cursor].kind != itemPane {
		t.Errorf("cursor should land on a pane, not a session header")
	}
}

func TestCursorStability_SessionHeaderPreserved(t *testing.T) {
	// If cursor is on a session header and items shift, it should stay on
	// the same session header.
	m := &tuiModel{
		verdicts: []model.Verdict{
			{Target: "alpha:1.0", Session: "alpha", Agent: "opencode", Blocked: true,
				Actions: []model.Action{{Keys: "1", Label: "opt"}}},
			{Target: "beta:1.0", Session: "beta", Agent: "opencode", Blocked: true,
				Actions: []model.Action{{Keys: "1", Label: "opt"}}},
		},
		expanded:        map[string]bool{"alpha": true, "beta": true},
		manualCollapsed: make(map[string]bool),
		width:           120,
		height:          40,
	}
	m.rebuildGroups()

	// Select the "beta" session header
	for i, item := range m.items {
		if item.kind == itemSession && item.session == "beta" {
			m.cursor = i
			break
		}
	}

	// Simulate scan that adds a new session before beta alphabetically
	prevKey := m.selectedItemKey()
	m.verdicts = []model.Verdict{
		{Target: "alpha:1.0", Session: "alpha", Agent: "opencode", Blocked: true,
			Actions: []model.Action{{Keys: "1", Label: "opt"}}},
		{Target: "alpha:2.0", Session: "alpha", Agent: "opencode", Blocked: true,
			Actions: []model.Action{{Keys: "1", Label: "opt"}}},
		{Target: "beta:1.0", Session: "beta", Agent: "opencode", Blocked: true,
			Actions: []model.Action{{Keys: "1", Label: "opt"}}},
	}
	m.rebuildGroups()
	m.restoreCursorByKey(prevKey)

	if m.items[m.cursor].kind != itemSession || m.items[m.cursor].session != "beta" {
		t.Errorf("expected cursor on beta session header, got cursor=%d kind=%v session=%s",
			m.cursor, m.items[m.cursor].kind, m.items[m.cursor].session)
	}
}

func TestFilterAgents_EventSourceNonBlockedHidden(t *testing.T) {
	m := &tuiModel{
		verdicts: []model.Verdict{
			{Target: "a:0.0", Session: "a", Agent: "claude", Blocked: false, EvalSource: model.EvalSourceEvent},
			{Target: "a:0.1", Session: "a", Agent: "claude", Blocked: true, EvalSource: model.EvalSourceEvent,
				Actions: []model.Action{{Keys: "Enter", Label: "ok"}}},
		},
		expanded:        map[string]bool{"a": true},
		manualCollapsed: make(map[string]bool),
		filter:          filterAgents,
	}
	m.rebuildGroups()

	paneCount := 0
	for _, it := range m.items {
		if it.kind == itemPane {
			paneCount++
		}
	}
	if paneCount != 1 {
		t.Fatalf("expected 1 pane in agents filter for event source, got %d", paneCount)
	}
}
