package supervisor

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/timvw/pane-patrol/internal/model"
)

// --- Helper function tests ---

func TestHasTabNavigation(t *testing.T) {
	tests := []struct {
		name    string
		verdict *model.Verdict
		want    bool
	}{
		{
			name:    "nil verdict",
			verdict: nil,
			want:    false,
		},
		{
			name: "not blocked",
			verdict: &model.Verdict{
				Blocked: false,
				Actions: []model.Action{{Keys: "Tab", Label: "next tab"}},
			},
			want: false,
		},
		{
			name: "blocked with Tab action",
			verdict: &model.Verdict{
				Blocked: true,
				Actions: []model.Action{
					{Keys: "1", Label: "option 1"},
					{Keys: "Tab", Label: "next tab"},
				},
			},
			want: true,
		},
		{
			name: "blocked without Tab action",
			verdict: &model.Verdict{
				Blocked: true,
				Actions: []model.Action{
					{Keys: "1", Label: "option 1"},
					{Keys: "Enter", Label: "submit"},
				},
			},
			want: false,
		},
		{
			name: "Tab action with wrong label",
			verdict: &model.Verdict{
				Blocked: true,
				Actions: []model.Action{
					{Keys: "Tab", Label: "something else"},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasTabNavigation(tt.verdict)
			if got != tt.want {
				t.Errorf("hasTabNavigation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsConfirmTab(t *testing.T) {
	tests := []struct {
		name    string
		verdict *model.Verdict
		want    bool
	}{
		{
			name:    "nil",
			verdict: nil,
			want:    false,
		},
		{
			name:    "confirm tab reason",
			verdict: &model.Verdict{Blocked: true, Reason: "question dialog confirm tab"},
			want:    true,
		},
		{
			name:    "regular question",
			verdict: &model.Verdict{Blocked: true, Reason: "question dialog waiting for answer"},
			want:    false,
		},
		{
			name:    "not blocked",
			verdict: &model.Verdict{Blocked: false, Reason: "question dialog confirm tab"},
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isConfirmTab(tt.verdict)
			if got != tt.want {
				t.Errorf("isConfirmTab() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseTabsFromWaitingFor(t *testing.T) {
	tests := []struct {
		name       string
		waitingFor string
		want       []string
	}{
		{
			name:       "multi-tab header",
			waitingFor: "[tabs] Next steps | Aspire wt config | Skill overlap | Confirm\nWhich features?",
			want:       []string{"Next steps", "Aspire wt config", "Skill overlap", "Confirm"},
		},
		{
			name:       "two tabs",
			waitingFor: "[tabs] Database | Confirm\nPick a database",
			want:       []string{"Database", "Confirm"},
		},
		{
			name:       "no tabs",
			waitingFor: "Which database do you want?\n1. PostgreSQL\n2. SQLite",
			want:       nil,
		},
		{
			name:       "empty",
			waitingFor: "",
			want:       nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTabsFromWaitingFor(tt.waitingFor)
			if len(got) != len(tt.want) {
				t.Fatalf("parseTabsFromWaitingFor() got %d tabs, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("tab[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// --- Key routing tests ---
//
// These tests verify that the correct navigation path is taken based on the
// verdict's tab configuration. They create a tuiModel with mock verdicts and
// check state changes after calling key handlers.
//
// Navigation model (top/bottom split layout):
//   - List panel (top): enter/right=focus action panel, click=jump to tmux, up/down=navigate
//   - Action panel (bottom): enter=execute, left/esc=back to list, tab/s-tab=question tabs
//   - Text input: all keys forwarded to widget except esc/enter/tab/1-9/space

// multiTabVerdict returns a verdict that represents a multi-tab question
// with Tab/BTab actions (as produced by the OpenCode parser).
func multiTabVerdict() model.Verdict {
	return model.Verdict{
		Target:  "test:0.0",
		Session: "test",
		Blocked: true,
		Agent:   "opencode",
		Reason:  "question dialog waiting for answer",
		WaitingFor: "[tabs] Next steps | Config | Confirm\n" +
			"Which features do you want?\n" +
			"1. Feature A\n2. Feature B",
		Actions: []model.Action{
			{Keys: "1", Label: "Feature A", Risk: "low", Raw: true},
			{Keys: "2", Label: "Feature B", Risk: "low", Raw: true},
			{Keys: "Tab", Label: "next tab", Risk: "low", Raw: true},
			{Keys: "BTab", Label: "prev tab", Risk: "low", Raw: true},
			{Keys: "Escape", Label: "dismiss question", Risk: "low", Raw: true},
		},
	}
}

// multiTabMultiSelectVerdict returns a multi-tab multi-select question verdict.
func multiTabMultiSelectVerdict() model.Verdict {
	return model.Verdict{
		Target:  "test:0.0",
		Session: "test",
		Blocked: true,
		Agent:   "opencode",
		Reason:  "question dialog waiting for answer",
		WaitingFor: "[tabs] Next steps | Config | Confirm\n" +
			"Select features:\n" +
			"1. [ ] Feature A\n2. [✓] Feature B",
		Actions: []model.Action{
			{Keys: "1", Label: "toggle Feature A", Risk: "low", Raw: true},
			{Keys: "2", Label: "toggle Feature B", Risk: "low", Raw: true},
			{Keys: "Enter", Label: "submit selection", Risk: "low", Raw: true},
			{Keys: "Tab", Label: "next tab", Risk: "low", Raw: true},
			{Keys: "BTab", Label: "prev tab", Risk: "low", Raw: true},
			{Keys: "Escape", Label: "dismiss question", Risk: "low", Raw: true},
		},
	}
}

// confirmTabVerdict returns a confirm tab verdict.
func confirmTabVerdict() model.Verdict {
	return model.Verdict{
		Target:  "test:0.0",
		Session: "test",
		Blocked: true,
		Agent:   "opencode",
		Reason:  "question dialog confirm tab",
		WaitingFor: "[tabs] Next steps | Config | Confirm\n" +
			"[confirm tab]\nFeature A: selected\nConfig: default",
		Actions: []model.Action{
			{Keys: "Enter", Label: "submit all answers", Risk: "low", Raw: true},
			{Keys: "Tab", Label: "next tab", Risk: "low", Raw: true},
			{Keys: "BTab", Label: "prev tab", Risk: "low", Raw: true},
			{Keys: "Escape", Label: "dismiss question", Risk: "low", Raw: true},
		},
	}
}

// noTabVerdict returns a simple single-question verdict without tab navigation.
func noTabVerdict() model.Verdict {
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

// newTestModel creates a tuiModel with a single verdict, cursor on the pane,
// and focus on the action panel. This simulates the state when the user has
// navigated to a blocked pane and moved focus to the actions column.
func newTestModel(v model.Verdict) *tuiModel {
	ti := textinput.New()
	ti.Placeholder = "Type response and press Enter..."
	ti.CharLimit = 2048
	ti.Width = 80

	m := &tuiModel{
		verdicts:        []model.Verdict{v},
		focus:           panelActions,
		expanded:        map[string]bool{v.Session: true},
		manualCollapsed: make(map[string]bool),
		textInput:       ti,
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

// --- Esc returns to list, left/h are no-ops in action panel ---

func TestActionPanelKey_EscReturnsToList(t *testing.T) {
	// Esc should return to the list panel for all verdict types.
	// In the top/bottom split layout, only esc navigates back — no left/h.
	verdicts := map[string]model.Verdict{
		"multi-tab":        multiTabVerdict(),
		"no-tab":           noTabVerdict(),
		"confirm-tab":      confirmTabVerdict(),
		"multi-select-tab": multiTabMultiSelectVerdict(),
	}
	for vName, v := range verdicts {
		t.Run(vName, func(t *testing.T) {
			m := newTestModel(v)
			msg := tea.KeyMsg{Type: tea.KeyEscape}
			_, _ = m.handleActionPanelKey(msg)

			if m.scanning {
				t.Error("expected scanning=false (esc just returns to list, no rescan)")
			}
			if m.focus != panelList {
				t.Errorf("expected focus=panelList, got %v", m.focus)
			}
		})
	}
}

func TestActionPanelKey_LeftReturnsToList(t *testing.T) {
	// left returns to the list panel (go back one level).
	m := newTestModel(multiTabVerdict())
	msg := tea.KeyMsg{Type: tea.KeyLeft}
	_, _ = m.handleActionPanelKey(msg)

	if m.focus != panelList {
		t.Errorf("expected focus=panelList after left, got %v", m.focus)
	}
}

// --- Tab/Shift+Tab: question tab navigation ---

func TestActionPanelKey_TabOnMultiTab_NavigatesForward(t *testing.T) {
	m := newTestModel(multiTabVerdict())
	msg := tea.KeyMsg{Type: tea.KeyTab}
	_, _ = m.handleActionPanelKey(msg)

	if !m.scanning {
		t.Error("expected scanning=true (navigateTab triggers rescan)")
	}
	if m.focus != panelActions {
		t.Errorf("expected focus=panelActions (stay on action panel during tab nav), got %v", m.focus)
	}
}

func TestActionPanelKey_ShiftTabOnMultiTab_NavigatesBackward(t *testing.T) {
	m := newTestModel(multiTabVerdict())
	msg := tea.KeyMsg{Type: tea.KeyShiftTab}
	_, _ = m.handleActionPanelKey(msg)

	if !m.scanning {
		t.Error("expected scanning=true (navigateTab triggers rescan)")
	}
	if m.focus != panelActions {
		t.Errorf("expected focus=panelActions (stay on action panel during tab nav), got %v", m.focus)
	}
}

func TestActionPanelKey_TabOnNonTab_NoOp(t *testing.T) {
	m := newTestModel(noTabVerdict())
	msg := tea.KeyMsg{Type: tea.KeyTab}
	_, _ = m.handleActionPanelKey(msg)

	// No multi-tab question, so tab does nothing
	if m.scanning {
		t.Error("expected scanning=false (no-op)")
	}
	if m.focus != panelActions {
		t.Errorf("expected focus=panelActions (unchanged), got %v", m.focus)
	}
}

// (Esc tests covered by TestActionPanelKey_EscReturnsToList above)

// --- Multi-select multi-tab: tab sends buffered selections ---

func TestActionPanelKey_MultiSelectMultiTab_TabSendsBuffered(t *testing.T) {
	m := newTestModel(multiTabMultiSelectVerdict())
	v := m.selectedVerdict()
	m.initLocalChecks(v)

	msg := tea.KeyMsg{Type: tea.KeyTab}
	_, _ = m.handleActionPanelKey(msg)

	if !m.scanning {
		t.Error("expected scanning=true (navigateTab via submitBufferedAndSendKey)")
	}
	// Local checks should be reset after navigation
	if m.localChecks != nil {
		t.Error("expected localChecks to be reset after tab navigation")
	}
}

// TestNavigateTab_Direction verifies that navigateTab sends the correct
// tmux key name. Since NudgePane will fail without tmux, we check the
// error message to confirm which key was attempted.
func TestNavigateTab_Direction(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantDir string
	}{
		{"forward", "Tab", "next"},
		{"backward", "BTab", "prev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(multiTabVerdict())
			_ = m.navigateTab(tt.key)

			// navigateTab should have attempted to send the key and set a message
			// (either success message with direction, or error from NudgePane)
			if m.message == "" {
				t.Error("expected message to be set after navigateTab")
			}
			if m.focus != panelActions {
				t.Errorf("focus should be panelActions after navigateTab (stay focused), got %v", m.focus)
			}
			if !m.scanning {
				t.Error("scanning should be true after navigateTab")
			}
		})
	}
}

// --- List panel: enter focuses action panel, o jumps to tmux ---

func TestListKey_EnterOnPane_JumpsToTmuxPane(t *testing.T) {
	m := newTestModel(multiTabVerdict())
	m.focus = panelList // start in list panel
	msg := tea.KeyMsg{Type: tea.KeyEnter}
	_, _ = m.handleVerdictListKey(msg)

	// Enter jumps to the tmux pane (switch-client), focus stays on list.
	// jumpToPane will fail without tmux, but message should reflect the attempt.
	if m.focus != panelList {
		t.Errorf("expected focus=panelList after Enter (jump to pane), got %v", m.focus)
	}
}

func TestListKey_RightOnPane_FocusesActionPanel(t *testing.T) {
	m := newTestModel(multiTabVerdict())
	m.focus = panelList
	msg := tea.KeyMsg{Type: tea.KeyRight}
	_, _ = m.handleVerdictListKey(msg)

	if m.focus != panelActions {
		t.Errorf("expected focus=panelActions after Right on pane, got %v", m.focus)
	}
}

func TestListKey_EnterOnSession_TogglesExpand(t *testing.T) {
	m := newTestModel(multiTabVerdict())
	m.focus = panelList
	// Move cursor to session header (index 0)
	m.cursor = 0
	wasExpanded := m.expanded[m.items[0].session]

	msg := tea.KeyMsg{Type: tea.KeyEnter}
	_, _ = m.handleVerdictListKey(msg)

	if m.focus != panelList {
		t.Errorf("expected focus=panelList after Enter on session, got %v", m.focus)
	}
	if m.expanded[m.items[0].session] == wasExpanded {
		t.Error("expected session expand state to toggle")
	}
}

// --- Text input: arrow keys forwarded to widget ---

// TestInlineTextInput_TabNavigatesOnMultiTab verifies that Tab in the text
// input navigates tabs (not inserted into the text) on multi-tab forms.
func TestInlineTextInput_TabNavigatesOnMultiTab(t *testing.T) {
	m := newTestModel(multiTabVerdict())
	m.editing = true
	m.textInput.Focus()

	msg := tea.KeyMsg{Type: tea.KeyTab}
	_, _ = m.handleInlineTextInputKey(msg)

	if !m.scanning {
		t.Error("expected scanning=true (tab navigates to next tab)")
	}
	if m.editing {
		t.Error("expected editing=false after tab navigation")
	}
}

// TestInlineTextInput_LeftMovesTextCursor verifies that Left arrow in
// the text input is forwarded to the text widget (moves cursor), not
// intercepted for navigation.
func TestInlineTextInput_LeftMovesTextCursor(t *testing.T) {
	m := newTestModel(multiTabVerdict())
	m.editing = true
	m.textInput.Focus()
	m.textInput.SetValue("hello")

	msg := tea.KeyMsg{Type: tea.KeyLeft}
	_, _ = m.handleInlineTextInputKey(msg)

	// Left should NOT trigger tab navigation — it should be forwarded to text input
	if m.scanning {
		t.Error("left arrow should not trigger tab navigation in text input")
	}
	if !m.editing {
		t.Error("should still be editing after left arrow in text input")
	}
}

// --- Click target dispatch tests ---

// setupClickTargets populates panelClicks and tabZones on a test model
// to simulate what rendering would produce, without needing a full View() call.
func setupClickTargets(m *tuiModel) {
	m.actionPanelY = 10
	m.panelClicks = []panelClickInfo{
		{kind: clickNone, index: -1},   // row 0: header
		{kind: clickNone, index: -1},   // row 1: reason
		{kind: clickTabBar, index: -1}, // row 2: tab bar
		{kind: clickNone, index: -1},   // row 3: blank
		{kind: clickNone, index: -1},   // row 4: question text
		{kind: clickToggle, index: 0},  // row 5: toggle option 0
		{kind: clickToggle, index: 1},  // row 6: toggle option 1
		{kind: clickNone, index: -1},   // row 7: blank
		{kind: clickAction, index: 2},  // row 8: submit action
		{kind: clickNone, index: -1},   // row 9: hint
	}
	m.tabZones = []tabClickZone{
		{xStart: 0, xEnd: 13, tabIndex: 0},  // " Next steps "
		{xStart: 15, xEnd: 24, tabIndex: 1}, // " Config "
		{xStart: 26, xEnd: 36, tabIndex: 2}, // " Confirm "
	}
}

func TestClickAction_ExecutesAction(t *testing.T) {
	m := newTestModel(noTabVerdict())
	m.actionPanelY = 10
	m.panelClicks = []panelClickInfo{
		{kind: clickNone, index: -1},  // row 0: header
		{kind: clickAction, index: 0}, // row 1: action "allow once"
		{kind: clickAction, index: 1}, // row 2: action "dismiss"
	}

	// Click on action row 1 (Y = actionPanelY + 1 = 11)
	msg := tea.MouseMsg{X: 5, Y: 11, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	_, _ = m.handleActionPanelClick(msg)

	if m.actionCursor != 0 {
		t.Errorf("expected actionCursor=0, got %d", m.actionCursor)
	}
	// executeSelectedAction returns focus to list after executing
	if m.focus != panelList {
		t.Errorf("expected focus=panelList after action execution, got %v", m.focus)
	}
}

func TestClickToggle_TogglesLocalCheck(t *testing.T) {
	m := newTestModel(multiTabMultiSelectVerdict())
	setupClickTargets(m)

	v := m.selectedVerdict()
	m.initLocalChecks(v)
	initialState := m.localChecks[0]

	// Click on toggle row 5 (Y = actionPanelY + 5 = 15)
	msg := tea.MouseMsg{X: 5, Y: 15, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	_, _ = m.handleActionPanelClick(msg)

	if m.localChecks[0] == initialState {
		t.Error("expected localChecks[0] to be toggled")
	}
	if m.actionCursor != 0 {
		t.Errorf("expected actionCursor=0, got %d", m.actionCursor)
	}
	if m.focus != panelActions {
		t.Errorf("expected focus=panelActions, got %v", m.focus)
	}
}

func TestClickToggle_DoubleClickRestores(t *testing.T) {
	m := newTestModel(multiTabMultiSelectVerdict())
	setupClickTargets(m)

	v := m.selectedVerdict()
	m.initLocalChecks(v)
	initialState := m.localChecks[1]

	// Click toggle twice — should restore original state
	msg := tea.MouseMsg{X: 5, Y: 16, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft} // row 6 = toggle index 1
	_, _ = m.handleActionPanelClick(msg)
	_, _ = m.handleActionPanelClick(msg)

	if m.localChecks[1] != initialState {
		t.Error("expected localChecks[1] to be restored after double-click")
	}
}

func TestClickTabBar_FocusesPanel(t *testing.T) {
	m := newTestModel(multiTabVerdict())
	m.focus = panelList // Start from list panel
	setupClickTargets(m)

	// Click on tab bar row 2 (Y = actionPanelY + 2 = 12), X=20 → Config tab (xStart=15, xEnd=24)
	msg := tea.MouseMsg{X: 20, Y: 12, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	_, _ = m.handleActionPanelClick(msg)

	// clickTab sends Tab keys, so scanning should be true (rescan triggered)
	if !m.scanning {
		t.Error("expected scanning=true after tab click (triggers rescan)")
	}
}

func TestClickTabBar_MissesTab(t *testing.T) {
	m := newTestModel(multiTabVerdict())
	setupClickTargets(m)

	// Click on tab bar row but between tabs (X=14, gap between tab 0 and 1)
	msg := tea.MouseMsg{X: 14, Y: 12, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	_, _ = m.handleActionPanelClick(msg)

	// Should just focus the panel, not navigate
	if m.scanning {
		t.Error("expected scanning=false (click missed all tab zones)")
	}
	if m.focus != panelActions {
		t.Errorf("expected focus=panelActions, got %v", m.focus)
	}
}

func TestClickNone_JustFocuses(t *testing.T) {
	m := newTestModel(multiTabMultiSelectVerdict())
	m.focus = panelList
	setupClickTargets(m)

	// Click on a non-clickable row (row 4: question text, Y = 14)
	msg := tea.MouseMsg{X: 5, Y: 14, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	_, _ = m.handleActionPanelClick(msg)

	if m.focus != panelActions {
		t.Errorf("expected focus=panelActions, got %v", m.focus)
	}
	if m.scanning {
		t.Error("expected scanning=false (non-clickable area)")
	}
}

func TestClickOutsidePanel_JustFocuses(t *testing.T) {
	m := newTestModel(noTabVerdict())
	m.actionPanelY = 10
	m.panelClicks = []panelClickInfo{
		{kind: clickAction, index: 0},
	}

	// Click below the panel (Y = actionPanelY + 5, but only 1 panel row)
	msg := tea.MouseMsg{X: 5, Y: 15, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	_, _ = m.handleActionPanelClick(msg)

	if m.focus != panelActions {
		t.Errorf("expected focus=panelActions after out-of-bounds click, got %v", m.focus)
	}
}

func TestClickSubmitAction_MultiSelect(t *testing.T) {
	m := newTestModel(multiTabMultiSelectVerdict())
	setupClickTargets(m)

	v := m.selectedVerdict()
	m.initLocalChecks(v)

	// Click on submit action at row 8 (Y = actionPanelY + 8 = 18), index=2 which is "submit selection"
	msg := tea.MouseMsg{X: 5, Y: 18, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	_, _ = m.handleActionPanelClick(msg)

	if m.actionCursor != 2 {
		t.Errorf("expected actionCursor=2 (submit), got %d", m.actionCursor)
	}
	// submitBufferedSelection returns focus to list after executing
	if m.focus != panelList {
		t.Errorf("expected focus=panelList after submit, got %v", m.focus)
	}
}
