package supervisor

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/timvw/pane-patrol/internal/model"
)

// Styles
var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	headerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("8"))
	blockedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
	activeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	riskLowStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	riskMedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	riskHighStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	statusStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	// Agent-specific dialog border styles.
	// OpenCode uses a thick "┃" left border in different colors:
	//   - warning/yellow for permission dialogs
	//   - accent/blue for question dialogs
	//   - error/red for reject dialogs
	warningBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow — OpenCode permission
	accentBorderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")) // blue — OpenCode question
	errorBorderStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red — OpenCode reject

	// Claude Code uses a muted style for tool details.
	claudeToolStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14")) // cyan — tool name
	claudeDimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // dim — details

	// Codex uses bold for titles and "$ " for commands.
	codexTitleStyle   = lipgloss.NewStyle().Bold(true)
	codexCommandStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green — commands
	codexCursorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("14")) // cyan — "›" cursor
)

// displayFilter controls which panes are visible in the TUI.
type displayFilter int

const (
	filterBlocked displayFilter = iota // only blocked agents
	filterAgents                       // all agent panes (blocked + active)
	filterAll                          // everything including non-agents
)

func (f displayFilter) String() string {
	switch f {
	case filterBlocked:
		return "blocked"
	case filterAgents:
		return "agents"
	case filterAll:
		return "all"
	default:
		return "?"
	}
}

func (f displayFilter) next() displayFilter {
	return (f + 1) % 3
}

// focusPanel tracks which panel has keyboard focus.
type focusPanel int

const (
	panelList    focusPanel = iota // left: session/pane list
	panelActions                   // right: action buttons
)

// listItem represents a row in the grouped verdict list.
// It is either a session header or an individual pane.
type listItem struct {
	kind    itemKind
	session string
	paneIdx int // index into verdicts slice (only for itemPane)
}

type itemKind int

const (
	itemSession itemKind = iota
	itemPane
)

// sessionGroup holds the verdicts for a single session.
type sessionGroup struct {
	name     string
	verdicts []int // indices into the flat verdicts slice
	blocked  int
	active   int
}

// messages
type scanResultMsg struct {
	result *ScanResult
	err    error
}

type tickMsg struct{}

// nudgeResultMsg is sent when async auto-nudge completes.
type nudgeResultMsg struct {
	messages []string // status messages describing what was sent
}

// TUI runs the interactive supervisor.
type TUI struct {
	Scanner          *Scanner
	RefreshInterval  time.Duration // 0 disables auto-refresh
	AutoNudge        bool          // Enable automatic nudging of blocked panes
	AutoNudgeMaxRisk string        // Maximum risk level to auto-nudge: "low", "medium", "high"
}

// model implements tea.Model
type tuiModel struct {
	scanner         *Scanner
	ctx             context.Context
	refreshInterval time.Duration
	verdicts        []model.Verdict
	cursor          int
	focus           focusPanel

	// display filter
	filter displayFilter

	// grouped list
	groups   []sessionGroup
	expanded map[string]bool // session name -> expanded
	items    []listItem      // visible items (rebuilt on verdicts/expand change)

	// action panel state
	actionCursor int  // selected action index (0-based) in the right panel
	editing      bool // true when inline text input is active in the action panel

	// text input state (rendered inline in the action panel)
	textInput textinput.Model

	// layout (computed in viewVerdictList, used for mouse hit testing)
	actionPanelX int // X offset where the action panel starts

	// dimensions
	width  int
	height int

	// status
	scanning  bool
	message   string
	scanCount int

	// auto-nudge
	autoNudge        bool   // whether auto-nudge is enabled (toggleable at runtime)
	autoNudgeMaxRisk string // maximum risk: "low", "medium", "high"

	// cumulative stats
	totalCacheHits int
}

func (t *TUI) Run(ctx context.Context) error {
	ti := textinput.New()
	ti.Placeholder = "Type response and press Enter..."
	ti.CharLimit = 2048
	ti.Width = 80

	maxRisk := t.AutoNudgeMaxRisk
	if maxRisk == "" {
		maxRisk = "low"
	}

	m := &tuiModel{
		scanner:          t.Scanner,
		ctx:              ctx,
		refreshInterval:  t.RefreshInterval,
		expanded:         make(map[string]bool),
		textInput:        ti,
		autoNudge:        t.AutoNudge,
		autoNudgeMaxRisk: maxRisk,
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (m *tuiModel) Init() tea.Cmd {
	m.scanning = true
	return m.doScan()
}

// scheduleTick returns a tea.Cmd that sends a tickMsg after the refresh interval.
// Returns nil if auto-refresh is disabled (interval <= 0).
func (m *tuiModel) scheduleTick() tea.Cmd {
	if m.refreshInterval <= 0 {
		return nil
	}
	return tea.Tick(m.refreshInterval, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m *tuiModel) doScan() tea.Cmd {
	scanner := m.scanner
	ctx := m.ctx
	return func() tea.Msg {
		result, err := scanner.Scan(ctx)
		return scanResultMsg{result: result, err: err}
	}
}

// rebuildGroups groups verdicts by session and rebuilds the visible items list.
// The display filter controls which panes are included:
//   - filterBlocked: only agent panes that are blocked
//   - filterAgents: all agent panes (blocked + active), excluding non-agents
//   - filterAll: everything including non-agent panes
func (m *tuiModel) rebuildGroups() {
	seen := map[string]int{} // session -> index in groups
	m.groups = nil
	for i, v := range m.verdicts {
		// Apply display filter
		switch m.filter {
		case filterBlocked:
			if !v.Blocked || v.Agent == "not_an_agent" || v.Agent == "error" {
				continue
			}
		case filterAgents:
			if v.Agent == "not_an_agent" {
				continue
			}
		case filterAll:
			// show everything
		}

		idx, ok := seen[v.Session]
		if !ok {
			idx = len(m.groups)
			seen[v.Session] = idx
			m.groups = append(m.groups, sessionGroup{name: v.Session})
		}
		m.groups[idx].verdicts = append(m.groups[idx].verdicts, i)
		if v.Blocked {
			m.groups[idx].blocked++
		}
		if v.Agent != "error" && v.Agent != "not_an_agent" && !v.Blocked {
			m.groups[idx].active++
		}
	}

	// Sort groups alphabetically for a stable, predictable order.
	// Blocked status is indicated by icons — no reordering on status change.
	sort.SliceStable(m.groups, func(i, j int) bool {
		return m.groups[i].name < m.groups[j].name
	})

	// Auto-expand sessions that have blocked panes (the ones that need
	// attention), and single-pane sessions (no benefit to collapsing).
	// Sessions with only active/non-blocked panes stay collapsed.
	// Users can still manually collapse/expand with Enter or left/right.
	for _, g := range m.groups {
		if len(g.verdicts) == 1 || g.blocked > 0 {
			m.expanded[g.name] = true
		}
	}

	m.rebuildItems()
}

// rebuildItems builds the flat visible items list from groups + expanded state.
func (m *tuiModel) rebuildItems() {
	m.items = nil
	for _, g := range m.groups {
		m.items = append(m.items, listItem{kind: itemSession, session: g.name})
		if m.expanded[g.name] {
			for _, vi := range g.verdicts {
				m.items = append(m.items, listItem{kind: itemPane, session: g.name, paneIdx: vi})
			}
		}
	}
}

// selectedVerdict returns the verdict for the currently selected item.
// For session headers: the first blocked pane, or the first pane.
// For pane items: that pane's verdict.
func (m *tuiModel) selectedVerdict() *model.Verdict {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return nil
	}
	item := m.items[m.cursor]
	if item.kind == itemPane {
		return &m.verdicts[item.paneIdx]
	}
	// Session header: find best pane
	for _, g := range m.groups {
		if g.name == item.session {
			// Prefer first blocked pane
			for _, vi := range g.verdicts {
				if m.verdicts[vi].Blocked {
					return &m.verdicts[vi]
				}
			}
			// Otherwise first pane
			if len(g.verdicts) > 0 {
				return &m.verdicts[g.verdicts[0]]
			}
		}
	}
	return nil
}

// selectedActionCount returns the number of actions available for the current selection.
func (m *tuiModel) selectedActionCount() int {
	v := m.selectedVerdict()
	if v == nil || !v.Blocked {
		return 0
	}
	n := len(v.Actions)
	if n > 9 {
		n = 9
	}
	return n
}

// clampActionCursor ensures actionCursor is within [0, count).
func (m *tuiModel) clampActionCursor() {
	count := m.selectedActionCount()
	if count == 0 {
		m.actionCursor = 0
		return
	}
	if m.actionCursor >= count {
		m.actionCursor = count - 1
	}
	if m.actionCursor < 0 {
		m.actionCursor = 0
	}
}

// executeSelectedAction sends the currently highlighted action to the target pane
// and triggers a rescan. Does NOT auto-jump to the target pane — the user stays
// in the supervisor TUI and can see the status message. Manual navigation via
// Enter/click is still available.
//
// Special case: "Type your own answer" / "None of the above" actions first send
// the digit key to select the custom option, then transition to text input mode
// so the user can type a freeform answer. The text is sent on Enter.
func (m *tuiModel) executeSelectedAction() tea.Cmd {
	v := m.selectedVerdict()
	if v == nil || !v.Blocked || m.actionCursor >= len(v.Actions) {
		return nil
	}
	action := v.Actions[m.actionCursor]

	// Custom answer actions ("Type your own answer", "None of the above"):
	// In single-select questions, selecting the custom answer option activates
	// inline text input (the full sequence digit → text → Enter is sent on submit).
	// In multi-select questions, number keys always toggle checkboxes — use 't'
	// to activate text input instead.
	if isCustomAnswerAction(action) && !isMultiSelectQuestion(v) {
		m.editing = true
		m.textInput.SetValue("")
		m.textInput.Placeholder = "Type your answer and press Enter..."
		m.textInput.Focus()
		return textinput.Blink
	}

	err := NudgePane(v.Target, action.Keys, action.Raw)
	if err != nil {
		m.message = fmt.Sprintf("Nudge failed: %v", err)
	} else {
		m.message = fmt.Sprintf("Sent '%s' to %s (%s)", action.Keys, v.Target, action.Label)
	}
	// Invalidate cache so the next scan re-evaluates this pane
	if m.scanner.Cache != nil {
		m.scanner.Cache.Invalidate(v.Target)
		m.scanner.Metrics.RecordCacheInvalidation(m.ctx)
	}
	// For multi-select toggles, keep focus on the action panel so the user
	// can continue toggling options. For all other actions, return to list.
	if isMultiSelectQuestion(v) && isToggleAction(action) {
		// Stay on the action panel — user is still toggling checkboxes
	} else {
		m.focus = panelList
	}
	m.scanning = true
	return m.doScan()
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case scanResultMsg:
		m.scanning = false
		if msg.err != nil {
			m.message = fmt.Sprintf("Scan error: %v", msg.err)
		} else if msg.result != nil {
			m.verdicts = msg.result.Verdicts
			m.scanCount++
			m.totalCacheHits += msg.result.CacheHits

			m.rebuildGroups()
			if m.cursor >= len(m.items) {
				m.cursor = 0
			}
			// Ensure cursor lands on a pane, not a session header
			for m.cursor < len(m.items)-1 && m.items[m.cursor].kind == itemSession {
				m.cursor++
			}
			m.clampActionCursor()
		}
		// Schedule next auto-refresh and auto-nudge (both async).
		var cmds []tea.Cmd
		if cmd := m.scheduleTick(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if m.focus != panelActions && !m.editing {
			if cmd := m.autoNudgeCmd(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)

	case nudgeResultMsg:
		if len(msg.messages) > 0 {
			m.message = strings.Join(msg.messages, " | ")
		}
		return m, nil

	case tickMsg:
		// Auto-refresh: skip if already scanning, focused on actions, or typing
		if m.scanning || m.editing || m.focus == panelActions {
			return m, m.scheduleTick()
		}
		m.scanning = true
		return m, m.doScan()
	}

	return m, nil
}

func (m *tuiModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Inline text input takes priority when active
	if m.editing {
		return m.handleInlineTextInputKey(msg)
	}
	if m.focus == panelActions {
		return m.handleActionPanelKey(msg)
	}
	return m.handleVerdictListKey(msg)
}

func (m *tuiModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.editing {
		return m, nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	// Determine if click is in the action panel (third column)
	if m.actionPanelX > 0 && msg.X >= m.actionPanelX {
		return m.handleActionPanelClick(msg)
	}

	// Click in the left panel: header line is row 0, items start at row 1
	clickedIdx := msg.Y - 1 // offset for header line
	if clickedIdx < 0 || clickedIdx >= len(m.items) {
		return m, nil
	}

	m.cursor = clickedIdx
	m.focus = panelList
	item := m.items[clickedIdx]
	if item.kind == itemPane {
		// Navigate tmux to this pane
		if errMsg := jumpToPane(m.verdicts[item.paneIdx].Target); errMsg != "" {
			m.message = errMsg
		}
	} else {
		// Session header: toggle expand/collapse
		m.expanded[item.session] = !m.expanded[item.session]
		m.rebuildItems()
		if m.expanded[item.session] && m.cursor+1 < len(m.items) {
			m.cursor++
		}
	}
	m.clampActionCursor()
	return m, nil
}

// handleActionPanelClick handles mouse clicks in the action panel (third column).
func (m *tuiModel) handleActionPanelClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	v := m.selectedVerdict()
	if v == nil || !v.Blocked || len(v.Actions) == 0 {
		return m, nil
	}

	// Action panel layout: row 1 = header line (Y=1 in terminal)
	// The action lines start after: target line + reason lines + blank separator
	// We compute the action line offset from the panel layout.
	reasonLines := len(wrapText(v.Reason, m.width*40/100))
	if reasonLines == 0 {
		reasonLines = 1
	}
	// Action panel rows (relative to first row in the panel area):
	// row 0: target header
	// rows 1..reasonLines: reason text
	// row reasonLines+1: blank separator (but only first blank)
	// rows reasonLines+2..: action lines (but row in panel = row in terminal - 1)
	actionStartRow := 1 + reasonLines + 1 // target + reason + blank

	// The panel starts at terminal row 1 (after the header line)
	clickedPanelRow := msg.Y - 1 // terminal row 1 = panel row 0
	actionIdx := clickedPanelRow - actionStartRow
	if actionIdx < 0 || actionIdx >= len(v.Actions) || actionIdx >= 9 {
		// Click wasn't on an action line — just move focus to action panel
		m.focus = panelActions
		m.clampActionCursor()
		return m, nil
	}

	// Execute the clicked action directly
	m.actionCursor = actionIdx
	m.focus = panelActions
	return m, m.executeSelectedAction()
}

func (m *tuiModel) handleVerdictListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "up", "k":
		if len(m.items) > 0 && m.cursor > 0 {
			m.cursor--
			// Skip session headers — only panes are actionable
			for m.cursor > 0 && m.items[m.cursor].kind == itemSession {
				m.cursor--
			}
			m.actionCursor = 0
			m.clampActionCursor()
		}

	case "down", "j":
		if len(m.items) > 0 && m.cursor < len(m.items)-1 {
			m.cursor++
			// Skip session headers — only panes are actionable
			for m.cursor < len(m.items)-1 && m.items[m.cursor].kind == itemSession {
				m.cursor++
			}
			m.actionCursor = 0
			m.clampActionCursor()
		}

	case "enter":
		if m.cursor < 0 || m.cursor >= len(m.items) {
			return m, nil
		}
		item := m.items[m.cursor]
		if item.kind == itemSession {
			// Toggle expand/collapse
			m.expanded[item.session] = !m.expanded[item.session]
			m.rebuildItems()
			if m.expanded[item.session] && m.cursor+1 < len(m.items) {
				m.cursor++
			}
			return m, nil
		}
		// Pane item: navigate tmux to this pane
		if errMsg := jumpToPane(m.verdicts[item.paneIdx].Target); errMsg != "" {
			m.message = errMsg
		}
		return m, nil

	case "right", "l", "tab":
		// Move focus to action panel if there are actions
		if m.selectedActionCount() > 0 {
			m.focus = panelActions
			v := m.selectedVerdict()
			if v != nil {
				m.actionCursor = v.Recommended
				// Auto-activate text input for custom answer dialogs
				if hasQuestionWithCustomAnswer(v) {
					m.editing = true
					m.textInput.SetValue("")
					m.textInput.Focus()
				}
			}
			m.clampActionCursor()
		} else if m.cursor >= 0 && m.cursor < len(m.items) {
			// No actions: Enter/right on session expands
			item := m.items[m.cursor]
			if item.kind == itemSession {
				m.expanded[item.session] = !m.expanded[item.session]
				m.rebuildItems()
				if m.expanded[item.session] && m.cursor+1 < len(m.items) {
					m.cursor++
				}
			}
		}
		return m, nil

	case "left", "h":
		// Collapse: if on a pane, jump to its session header
		// If on a session, collapse it
		if m.cursor < 0 || m.cursor >= len(m.items) {
			return m, nil
		}
		item := m.items[m.cursor]
		if item.kind == itemPane {
			// Find the session header above
			for i := m.cursor - 1; i >= 0; i-- {
				if m.items[i].kind == itemSession && m.items[i].session == item.session {
					m.cursor = i
					break
				}
			}
			return m, nil
		}
		// On session header: collapse
		if m.expanded[item.session] {
			m.expanded[item.session] = false
			m.rebuildItems()
			if m.cursor >= len(m.items) {
				m.cursor = len(m.items) - 1
			}
			return m, nil
		}

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// Quick-execute the Nth action for the currently selected pane
		v := m.selectedVerdict()
		if v == nil || !v.Blocked {
			return m, nil
		}
		idx := int(msg.String()[0] - '1') // 0-based
		if idx >= len(v.Actions) {
			return m, nil
		}
		m.actionCursor = idx
		return m, m.executeSelectedAction()

	case "t":
		// Open inline text input in the action panel
		if m.cursor >= 0 && m.cursor < len(m.items) {
			v := m.selectedVerdict()
			if v != nil {
				m.editing = true
				m.focus = panelActions
				m.textInput.SetValue("")
				m.textInput.Placeholder = "Type response and press Enter..."
				m.textInput.Focus()
				return m, textinput.Blink
			}
		}
		return m, nil

	case "a":
		// Toggle auto-nudge
		m.autoNudge = !m.autoNudge
		if m.autoNudge {
			m.message = fmt.Sprintf("Auto-nudge ON (max risk: %s)", m.autoNudgeMaxRisk)
		} else {
			m.message = "Auto-nudge OFF"
		}
		return m, nil

	case "f":
		// Cycle display filter: blocked -> agents -> all -> blocked
		m.filter = m.filter.next()
		m.message = fmt.Sprintf("Filter: %s", m.filter)
		m.rebuildGroups()
		m.cursor = 0
		// Ensure cursor lands on a pane, not a session header
		for m.cursor < len(m.items)-1 && m.items[m.cursor].kind == itemSession {
			m.cursor++
		}
		m.clampActionCursor()
		return m, nil

	case "r":
		// Rescan
		m.scanning = true
		m.message = ""
		return m, m.doScan()
	}

	return m, nil
}

// handleActionPanelKey handles keyboard input when the action panel has focus.
func (m *tuiModel) handleActionPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "esc", "escape", "left", "h":
		// Return focus to the list panel
		m.focus = panelList
		return m, nil

	case "up", "k":
		if m.actionCursor > 0 {
			m.actionCursor--
		}

	case "down", "j":
		count := m.selectedActionCount()
		if m.actionCursor < count-1 {
			m.actionCursor++
		}

	case "enter":
		return m, m.executeSelectedAction()

	case " ":
		// Spacebar toggles the current item in multi-select checklists.
		v := m.selectedVerdict()
		if v != nil && isMultiSelectQuestion(v) && m.actionCursor < len(v.Actions) {
			action := v.Actions[m.actionCursor]
			if isToggleAction(action) {
				return m, m.executeSelectedAction()
			}
		}
		return m, nil

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		v := m.selectedVerdict()
		if v == nil || !v.Blocked {
			return m, nil
		}
		idx := int(msg.String()[0] - '1')
		if idx >= len(v.Actions) {
			return m, nil
		}
		m.actionCursor = idx
		return m, m.executeSelectedAction()

	case "t":
		// Open inline text input (stays in action panel)
		v := m.selectedVerdict()
		if v != nil {
			m.editing = true
			m.textInput.SetValue("")
			m.textInput.Placeholder = "Type response and press Enter..."
			m.textInput.Focus()
			return m, textinput.Blink
		}
		return m, nil

	case "r":
		m.focus = panelList
		m.scanning = true
		m.message = ""
		return m, m.doScan()
	}

	return m, nil
}

// handleInlineTextInputKey handles keys when the inline text input is active
// in the action panel. Most keys are forwarded to the text input widget, but
// certain keys are intercepted to allow interaction with the action panel:
//   - Number keys 1-9: toggle checkboxes (multi-select) or execute actions
//   - Arrow keys up/down: navigate the action cursor
//   - Space: toggle current checkbox (multi-select)
//   - Enter: submit text (if non-empty) or submit selection (if empty)
//   - Escape: close text input and return to list
func (m *tuiModel) handleInlineTextInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "escape":
		m.editing = false
		m.textInput.Blur()
		m.textInput.SetValue("")
		m.focus = panelList
		return m, nil

	case "up", "k":
		if m.actionCursor > 0 {
			m.actionCursor--
		}
		return m, nil

	case "down", "j":
		count := m.selectedActionCount()
		if m.actionCursor < count-1 {
			m.actionCursor++
		}
		return m, nil

	case " ":
		// Space toggles the current checkbox in multi-select
		v := m.selectedVerdict()
		if v != nil && isMultiSelectQuestion(v) && m.actionCursor < len(v.Actions) {
			action := v.Actions[m.actionCursor]
			if isToggleAction(action) {
				err := NudgePane(v.Target, action.Keys, action.Raw)
				if err != nil {
					m.message = fmt.Sprintf("Nudge failed: %v", err)
				}
				if m.scanner.Cache != nil {
					m.scanner.Cache.Invalidate(v.Target)
					m.scanner.Metrics.RecordCacheInvalidation(m.ctx)
				}
				m.scanning = true
				return m, m.doScan()
			}
		}
		// Otherwise forward space to text input
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// Number keys toggle checkboxes / execute actions
		v := m.selectedVerdict()
		if v != nil && v.Blocked {
			idx := int(msg.String()[0] - '1')
			if idx < len(v.Actions) {
				action := v.Actions[idx]
				m.actionCursor = idx
				err := NudgePane(v.Target, action.Keys, action.Raw)
				if err != nil {
					m.message = fmt.Sprintf("Nudge failed: %v", err)
				} else {
					m.message = fmt.Sprintf("Sent '%s' to %s (%s)", action.Keys, v.Target, action.Label)
				}
				if m.scanner.Cache != nil {
					m.scanner.Cache.Invalidate(v.Target)
					m.scanner.Metrics.RecordCacheInvalidation(m.ctx)
				}
				m.scanning = true
				return m, m.doScan()
			}
		}
		return m, nil

	case "enter":
		text := m.textInput.Value()
		v := m.selectedVerdict()
		if text != "" && v != nil {
			target := v.Target

			// For question dialogs with a custom answer option, send:
			// 1. The digit key to select the custom option in the agent's TUI
			// 2. The typed text (into the agent's inline textarea)
			// 3. Enter to confirm the answer
			if customAction := findCustomAnswerAction(v); customAction != nil {
				if err := NudgePane(target, customAction.Keys, customAction.Raw); err != nil {
					m.message = fmt.Sprintf("Select custom option failed: %v", err)
				} else if err := NudgePane(target, text, false); err != nil {
					m.message = fmt.Sprintf("Send text failed: %v", err)
				} else if err := NudgePane(target, "Enter", true); err != nil {
					m.message = fmt.Sprintf("Send Enter failed: %v", err)
				} else {
					m.message = fmt.Sprintf("Answered '%s' to %s", truncate(text, 40), target)
				}
			} else {
				// Plain text input (not a question dialog)
				if err := NudgePane(target, text, false); err != nil {
					m.message = fmt.Sprintf("Send failed: %v", err)
				} else {
					m.message = fmt.Sprintf("Sent '%s' to %s", truncate(text, 40), target)
				}
			}

			// Invalidate cache so the next scan re-evaluates this pane
			if m.scanner.Cache != nil {
				m.scanner.Cache.Invalidate(target)
				m.scanner.Metrics.RecordCacheInvalidation(m.ctx)
			}
		}
		m.editing = false
		m.textInput.Blur()
		m.textInput.SetValue("")
		// Rescan after sending input
		m.scanning = true
		return m, m.doScan()
	}

	// Forward all other keys to the text input component
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m *tuiModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	return m.viewVerdictList()
}

func (m *tuiModel) viewVerdictList() string {
	var b strings.Builder

	// Header: title + keybindings + token usage
	b.WriteString(titleStyle.Render("Pane Supervisor"))
	b.WriteString("  ")
	if m.focus == panelActions {
		b.WriteString(dimStyle.Render("↑↓=select  Enter/click=execute  t=type  Esc/←=back  q=quit"))
	} else {
		autoLabel := "a=auto:OFF"
		if m.autoNudge {
			autoLabel = fmt.Sprintf("a=auto:ON(%s)", m.autoNudgeMaxRisk)
		}
		filterLabel := fmt.Sprintf("f=%s", m.filter)
		b.WriteString(dimStyle.Render(fmt.Sprintf("Enter/click=jump  →/Tab=actions  1-9=action  t=type  %s  %s  r=rescan  q=quit", filterLabel, autoLabel)))
	}
	if m.totalCacheHits > 0 {
		b.WriteString("  ")
		b.WriteString(dimStyle.Render(fmt.Sprintf("eval cache: %d", m.totalCacheHits)))
	}
	if m.scanning {
		b.WriteString("  ")
		b.WriteString(blockedStyle.Render("scanning..."))
	}
	b.WriteString("\n")

	if len(m.items) == 0 && m.scanning {
		b.WriteString("  Scanning panes...\n")
		return b.String()
	}

	if len(m.items) == 0 {
		b.WriteString("  No panes found.\n")
		return b.String()
	}

	// Layout widths: name | reason | actions
	nameWidth := 10
	for _, g := range m.groups {
		if len(g.name)+6 > nameWidth {
			nameWidth = len(g.name) + 6
		}
	}
	nameWidth += 6 // icon + indent + cursor + padding

	separator := " | "
	sepWidth := len(separator)

	// Actions panel gets 40% of width
	actionWidth := m.width * 40 / 100
	if actionWidth < 20 {
		actionWidth = 20
	}

	// Reason gets whatever is left
	reasonWidth := m.width - nameWidth - actionWidth - sepWidth*2
	if reasonWidth < 15 {
		reasonWidth = 15
	}

	// Store the X offset where the action panel starts (for mouse hit testing)
	m.actionPanelX = nameWidth + sepWidth + reasonWidth + sepWidth

	// Count totals
	totalBlocked := 0
	totalActive := 0
	for _, g := range m.groups {
		totalBlocked += g.blocked
		totalActive += g.active
	}

	// Available lines for the panels
	panelHeight := m.height - 3
	if panelHeight < 3 {
		panelHeight = 3
	}

	// Calculate visible window for scrolling
	maxVisible := panelHeight - 1
	if maxVisible < 2 {
		maxVisible = 2
	}
	if maxVisible > len(m.items) {
		maxVisible = len(m.items)
	}

	// Compute scroll window [start, end) that keeps cursor visible
	start := 0
	end := maxVisible
	if m.cursor >= end {
		end = m.cursor + 1
		start = end - maxVisible
	}
	if start < 0 {
		start = 0
		end = maxVisible
	}

	// Build action panel lines for the right column
	actionLines := m.buildActionPanel(actionWidth)

	// Render rows
	sep := headerStyle.Render(separator)
	row := 0
	for i := start; i < end && i < len(m.items); i++ {
		item := m.items[i]
		var nameCol, reasonCol string

		if item.kind == itemSession {
			nameCol, reasonCol = m.renderSessionRow(item, i, nameWidth, reasonWidth)
		} else {
			nameCol, reasonCol = m.renderPaneRow(item, i, nameWidth, reasonWidth)
		}

		// Action column
		actionCol := ""
		if row < len(actionLines) {
			actionCol = actionLines[row]
		}

		b.WriteString(nameCol)
		b.WriteString(sep)
		b.WriteString(reasonCol)
		b.WriteString(sep)
		b.WriteString(actionCol)
		b.WriteString("\n")
		row++
	}

	// Fill remaining panel height with action-only rows
	for row < panelHeight-1 {
		actionCol := ""
		if row < len(actionLines) {
			actionCol = actionLines[row]
		}
		if actionCol != "" {
			b.WriteString(padRight("", nameWidth))
			b.WriteString(sep)
			b.WriteString(padRight("", reasonWidth))
			b.WriteString(sep)
			b.WriteString(actionCol)
			b.WriteString("\n")
		}
		row++
	}

	// Summary line
	visiblePanes := 0
	for _, g := range m.groups {
		visiblePanes += len(g.verdicts)
	}
	summary := fmt.Sprintf("  %d blocked | %d active | %d/%d panes | scan #%d",
		totalBlocked, totalActive, visiblePanes, len(m.verdicts), m.scanCount)
	if start > 0 || end < len(m.items) {
		summary += fmt.Sprintf(" | showing %d-%d", start+1, end)
	}
	b.WriteString(dimStyle.Render(summary))
	b.WriteString("\n")

	// Status message
	if m.message != "" {
		b.WriteString(statusStyle.Render("  " + m.message))
		b.WriteString("\n")
	}

	return b.String()
}

// buildActionPanel builds the lines for the right-hand action panel showing
// the question and available actions for the currently selected pane.
// When the action panel has focus, the selected action is highlighted.
func (m *tuiModel) buildActionPanel(width int) []string {
	v := m.selectedVerdict()
	if v == nil {
		return nil
	}

	var lines []string

	// Target header
	lines = append(lines, dimStyle.Render(truncate(v.Target, width)))

	if !v.Blocked {
		lines = append(lines, activeStyle.Render("Active"))
		if v.Reason != "" {
			for _, rl := range wrapText(v.Reason, width) {
				lines = append(lines, dimStyle.Render(rl))
			}
		}
		return lines
	}

	// Multi-select questions: render interactive checklist instead of action buttons.
	if isMultiSelectQuestion(v) {
		return m.buildMultiSelectPanel(v, width, lines)
	}

	// Blocked: render agent-specific dialog representation
	lines = append(lines, renderDialogContent(v, width)...)
	lines = append(lines, "") // blank separator

	// Actions with number keys — highlight selected when panel is focused
	focused := m.focus == panelActions
	for i, a := range v.Actions {
		if i >= 9 {
			break
		}

		rec := " "
		if i == v.Recommended {
			rec = "*"
		}

		riskStr := riskLabel(a.Risk)
		label := fmt.Sprintf(" %s[%d] '%s' %s (%s)", rec, i+1, a.Keys, a.Label, riskStr)
		label = truncate(label, width)

		if focused && i == m.actionCursor {
			lines = append(lines, selectedStyle.Render(padRight("→"+label[1:], width)))
		} else {
			lines = append(lines, label)
		}
	}

	// Show inline text input for question dialogs with a custom answer option
	// (always visible, no need to activate the option first), or when
	// explicitly editing via 't' key for other panes.
	if hasQuestionWithCustomAnswer(v) || m.editing {
		lines = append(lines, m.renderInlineTextInput(width)...)
	} else if focused {
		lines = append(lines, "")
		lines = append(lines, dimStyle.Render("  t = type custom response"))
	}

	return lines
}

// renderInlineTextInput renders the text input widget inline as action panel
// lines. This is a single generic renderer used for all text input scenarios:
// custom answers in multi-select questions, custom answers in single-select
// questions, and freeform text input activated via 't'.
func (m *tuiModel) renderInlineTextInput(width int) []string {
	var lines []string
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(truncate("  Type response:", width)))

	// Render the text input widget (includes cursor when focused)
	inputView := m.textInput.View()
	lines = append(lines, "  "+inputView)

	if m.editing {
		lines = append(lines, dimStyle.Render("  enter=submit  esc=cancel"))
	}

	return lines
}

// renderDialogContent produces agent-specific styled lines for the dialog
// that the agent is blocked on. Dispatches based on v.Agent and v.Reason to
// replicate the visual style of each agent's TUI dialogs.
func renderDialogContent(v *model.Verdict, width int) []string {
	if v.Agent == "opencode" {
		return renderOpenCodeDialog(v, width)
	}
	if v.Agent == "claude_code" {
		return renderClaudeCodeDialog(v, width)
	}
	if v.Agent == "codex" {
		return renderCodexDialog(v, width)
	}
	return renderGenericDialog(v, width)
}

// renderOpenCodeDialog renders OpenCode dialogs with the characteristic "┃"
// left border in the appropriate color (yellow=permission, blue=question, red=reject).
//
// Source: packages/opencode/src/cli/cmd/tui/component/border.tsx
// SplitBorder uses "┃" (thick vertical) as left border.
func renderOpenCodeDialog(v *model.Verdict, width int) []string {
	var lines []string
	border := "┃ "
	borderWidth := 2

	reason := strings.ToLower(v.Reason)
	var borderStyle lipgloss.Style
	switch {
	case strings.Contains(reason, "permission"):
		borderStyle = warningBorderStyle
		// Title line: △ Permission required
		lines = append(lines, borderStyle.Render(border)+blockedStyle.Render("△ Permission required"))
	case strings.Contains(reason, "reject"):
		borderStyle = errorBorderStyle
		lines = append(lines, borderStyle.Render(border)+errorStyle.Render("△ Reject permission"))
	case strings.Contains(reason, "question"):
		borderStyle = accentBorderStyle
		// No special title — question text comes from WaitingFor
	default:
		// Idle or other — use dim border
		borderStyle = dimStyle
	}

	// Render WaitingFor content with the colored border prefix
	content := v.WaitingFor
	if content == "" {
		content = v.Reason
	}
	contentWidth := width - borderWidth
	if contentWidth < 10 {
		contentWidth = 10
	}
	for _, wl := range strings.Split(content, "\n") {
		for _, rl := range wrapText(wl, contentWidth) {
			lines = append(lines, borderStyle.Render(border)+rl)
		}
	}

	// Show the one-line reason as context below the dialog
	if v.WaitingFor != "" && v.Reason != "" {
		lines = append(lines, dimStyle.Render(truncate(v.Reason, width)))
	}

	return lines
}

// renderClaudeCodeDialog renders Claude Code dialogs without box borders —
// Claude Code uses a full-screen overlay with tool name, command details,
// and "❯" cursor on options.
//
// Source: binary analysis of /opt/homebrew/Caskroom/claude-code/*/claude
// Permission dialog: "Claude needs your permission to use {tool}"
// Edit approval: "Do you want to make this edit to {filename}?"
func renderClaudeCodeDialog(v *model.Verdict, width int) []string {
	var lines []string

	reason := strings.ToLower(v.Reason)
	switch {
	case strings.Contains(reason, "permission"):
		lines = append(lines, claudeToolStyle.Render("Permission required"))
	case strings.Contains(reason, "edit"):
		lines = append(lines, claudeToolStyle.Render("Edit approval"))
	case strings.Contains(reason, "idle"):
		lines = append(lines, claudeDimStyle.Render("Idle at prompt"))
	default:
		lines = append(lines, claudeToolStyle.Render("Waiting"))
	}

	// Render WaitingFor content — Claude Code dialog details
	content := v.WaitingFor
	if content == "" {
		content = v.Reason
	}
	for _, wl := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(wl)
		// Highlight command lines (start with "$")
		if strings.HasPrefix(trimmed, "$ ") {
			for _, rl := range wrapText(wl, width) {
				lines = append(lines, codexCommandStyle.Render(rl))
			}
		} else {
			lines = append(lines, wrapText(wl, width)...)
		}
	}

	// Show reason as context
	if v.WaitingFor != "" && v.Reason != "" {
		lines = append(lines, dimStyle.Render(truncate(v.Reason, width)))
	}

	return lines
}

// renderCodexDialog renders Codex dialogs with bold titles, "$ " commands
// in green, and "›" selection cursor.
//
// Source: codex-rs/tui/src/bottom_pane/approval_overlay.rs
// Title in bold, "$ command" prefix, "Reason: " prefix in italic,
// "›" selection cursor on options.
func renderCodexDialog(v *model.Verdict, width int) []string {
	var lines []string

	reason := strings.ToLower(v.Reason)
	switch {
	case strings.Contains(reason, "command approval"):
		lines = append(lines, codexTitleStyle.Render("Command approval"))
	case strings.Contains(reason, "edit approval"):
		lines = append(lines, codexTitleStyle.Render("Edit approval"))
	case strings.Contains(reason, "network"):
		lines = append(lines, codexTitleStyle.Render("Network access"))
	case strings.Contains(reason, "mcp"):
		lines = append(lines, codexTitleStyle.Render("MCP approval"))
	case strings.Contains(reason, "question"):
		lines = append(lines, codexTitleStyle.Render("Question"))
	case strings.Contains(reason, "user input"):
		lines = append(lines, codexTitleStyle.Render("User input requested"))
	case strings.Contains(reason, "idle"):
		lines = append(lines, dimStyle.Render("Idle at prompt"))
	default:
		lines = append(lines, codexTitleStyle.Render("Waiting"))
	}

	// Render WaitingFor content with Codex styling
	content := v.WaitingFor
	if content == "" {
		content = v.Reason
	}
	for _, wl := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(wl)
		switch {
		case strings.HasPrefix(trimmed, "$ "):
			// Command with $ prefix in green
			for _, rl := range wrapText(wl, width) {
				lines = append(lines, codexCommandStyle.Render(rl))
			}
		case strings.HasPrefix(trimmed, "› ") || strings.HasPrefix(trimmed, "›"):
			// Selection cursor in cyan
			for _, rl := range wrapText(wl, width) {
				lines = append(lines, codexCursorStyle.Render(rl))
			}
		default:
			lines = append(lines, wrapText(wl, width)...)
		}
	}

	// Show reason as context
	if v.WaitingFor != "" && v.Reason != "" {
		lines = append(lines, dimStyle.Render(truncate(v.Reason, width)))
	}

	return lines
}

// renderGenericDialog renders a generic blocked dialog for unknown agents.
// Falls back to the previous behavior: WaitingFor in yellow, reason in dim.
func renderGenericDialog(v *model.Verdict, width int) []string {
	var lines []string
	if v.WaitingFor != "" {
		for _, wl := range strings.Split(v.WaitingFor, "\n") {
			for _, rl := range wrapText(wl, width) {
				lines = append(lines, blockedStyle.Render(rl))
			}
		}
	} else if v.Reason != "" {
		for _, rl := range wrapText(v.Reason, width) {
			lines = append(lines, blockedStyle.Render(rl))
		}
	}
	if v.WaitingFor != "" && v.Reason != "" {
		lines = append(lines, dimStyle.Render(truncate(v.Reason, width)))
	}
	return lines
}

// isToggleAction returns true if the action is a single-digit key (1-9),
// indicating it toggles a multi-select checkbox rather than submitting.
func isToggleAction(a model.Action) bool {
	return len(a.Keys) == 1 && a.Keys[0] >= '1' && a.Keys[0] <= '9'
}

// isCustomAnswerAction returns true if the action represents a "type your
// own answer" option. Both OpenCode ("Type your own answer") and Codex
// ("None of the above") use freeform text entry for these options.
func isCustomAnswerAction(a model.Action) bool {
	lower := strings.ToLower(a.Label)
	return strings.Contains(lower, "type your own") ||
		strings.Contains(lower, "none of the above")
}

// findCustomAnswerAction returns the custom answer action from a verdict's
// action list, or nil if none exists.
func findCustomAnswerAction(v *model.Verdict) *model.Action {
	if v == nil {
		return nil
	}
	for i := range v.Actions {
		if isCustomAnswerAction(v.Actions[i]) {
			return &v.Actions[i]
		}
	}
	return nil
}

// hasQuestionWithCustomAnswer returns true if the verdict is a question
// dialog that has a "Type your own answer" / "None of the above" option.
func hasQuestionWithCustomAnswer(v *model.Verdict) bool {
	if v == nil || !v.Blocked || !strings.Contains(v.Reason, "question") {
		return false
	}
	return findCustomAnswerAction(v) != nil
}

// isMultiSelectQuestion returns true if the verdict represents a multi-select
// question dialog (options have [✓]/[ ] checkbox prefixes).
func isMultiSelectQuestion(v *model.Verdict) bool {
	if !v.Blocked || !strings.Contains(v.Reason, "question") {
		return false
	}
	return strings.Contains(v.WaitingFor, "[ ]") || strings.Contains(v.WaitingFor, "[✓]")
}

// checklistItem represents a parsed option from a multi-select question.
type checklistItem struct {
	label       string
	description string
	checked     bool
}

// parseChecklist splits WaitingFor content into question text and checkbox items.
func parseChecklist(waitingFor string) (questionText string, items []checklistItem) {
	wfLines := strings.Split(waitingFor, "\n")

	// Find first numbered option
	firstOptIdx := -1
	for i, line := range wfLines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) >= 2 && trimmed[0] >= '1' && trimmed[0] <= '9' && trimmed[1] == '.' {
			firstOptIdx = i
			break
		}
	}
	if firstOptIdx < 0 {
		return waitingFor, nil
	}

	// Question text = everything before first option
	var qLines []string
	for i := 0; i < firstOptIdx; i++ {
		trimmed := strings.TrimSpace(wfLines[i])
		if trimmed != "" {
			qLines = append(qLines, trimmed)
		}
	}
	questionText = strings.Join(qLines, "\n")

	// Parse options with descriptions
	for i := firstOptIdx; i < len(wfLines); i++ {
		trimmed := strings.TrimSpace(wfLines[i])
		if len(trimmed) >= 2 && trimmed[0] >= '1' && trimmed[0] <= '9' && trimmed[1] == '.' {
			item := checklistItem{
				label:   trimmed,
				checked: strings.Contains(trimmed, "[✓]"),
			}
			// Next line is description if indented (starts with spaces in the WaitingFor)
			if i+1 < len(wfLines) {
				next := wfLines[i+1]
				nextTrimmed := strings.TrimSpace(next)
				// Description lines are indented with "  " in WaitingFor
				if strings.HasPrefix(next, "  ") && nextTrimmed != "" &&
					(len(nextTrimmed) < 2 || nextTrimmed[0] < '1' || nextTrimmed[0] > '9' || nextTrimmed[1] != '.') {
					item.description = nextTrimmed
					i++ // skip description line
				}
			}
			items = append(items, item)
		}
	}

	return questionText, items
}

// buildMultiSelectPanel renders a multi-select question as an interactive
// checklist. Options are navigable with up/down, togglable with Enter or
// number keys, with Submit and Dismiss at the bottom.
func (m *tuiModel) buildMultiSelectPanel(v *model.Verdict, width int, lines []string) []string {
	focused := m.focus == panelActions
	borderStyle := accentBorderStyle
	border := "┃ "
	borderWidth := 2
	contentWidth := width - borderWidth
	if contentWidth < 10 {
		contentWidth = 10
	}

	// Parse question text and checkbox options
	questionText, checkItems := parseChecklist(v.WaitingFor)

	// Question text with blue border
	if questionText != "" {
		for _, wl := range wrapText(questionText, contentWidth) {
			lines = append(lines, borderStyle.Render(border)+wl)
		}
	}
	lines = append(lines, "")

	// Render each option as a navigable checkbox line.
	// actionCursor 0..N-1 maps to toggle actions, N+ maps to submit/dismiss.
	// For the custom answer option, render the text input directly below it.
	hasCustom := hasQuestionWithCustomAnswer(v)
	textInputRendered := false
	for i, item := range checkItems {
		if i >= len(v.Actions) {
			break
		}

		isCustomOpt := hasCustom && isCustomAnswerAction(v.Actions[i])

		// Checkbox indicator
		checkbox := "[ ]"
		checkStyle := dimStyle
		if item.checked {
			checkbox = "[✓]"
			checkStyle = activeStyle
		}

		optLabel := item.label
		// Strip the "N. [ ] " or "N. [✓] " prefix for clean display
		if idx := strings.Index(optLabel, "] "); idx >= 0 {
			optLabel = optLabel[idx+2:]
		} else if len(optLabel) > 3 {
			// "N. label" without checkbox
			optLabel = optLabel[3:]
		}
		optLabel = strings.TrimSpace(optLabel)

		line := fmt.Sprintf(" %s %d. %s", checkbox, i+1, optLabel)
		line = truncate(line, width)

		if focused && m.actionCursor == i {
			lines = append(lines, selectedStyle.Render(padRight("→"+line[1:], width)))
		} else {
			// line = " [ ] 1. label" — style the leading " [ ]" (4 chars), then append the rest unstyled.
			lines = append(lines, checkStyle.Render(line[:4])+line[4:])
		}

		// Description in dim, indented
		if item.description != "" && !isCustomOpt {
			desc := "      " + item.description
			desc = truncate(desc, width)
			lines = append(lines, dimStyle.Render(desc))
		}

		// Render the generic text input under the custom answer option
		if isCustomOpt {
			lines = append(lines, m.renderInlineTextInput(width)...)
			textInputRendered = true
		}
	}

	// Fallback: if the text input wasn't rendered under a custom option
	// (e.g., option truncated from WaitingFor), show it when editing via 't'.
	if m.editing && !textInputRendered {
		lines = append(lines, m.renderInlineTextInput(width)...)
	}

	lines = append(lines, "")

	// Submit and Dismiss actions (after toggle options in the actions array)
	toggleCount := len(checkItems)
	if toggleCount > len(v.Actions) {
		toggleCount = len(v.Actions)
	}
	for i := toggleCount; i < len(v.Actions) && i < toggleCount+4; i++ {
		a := v.Actions[i]
		rec := " "
		if i == v.Recommended {
			rec = "*"
		}
		label := fmt.Sprintf(" %s[%s] %s", rec, a.Keys, a.Label)
		label = truncate(label, width)

		if focused && m.actionCursor == i {
			lines = append(lines, selectedStyle.Render(padRight("→"+label[1:], width)))
		} else {
			lines = append(lines, label)
		}
	}

	// Hint line
	if focused {
		lines = append(lines, "")
		optCount := len(checkItems)
		if hasCustom {
			// Don't count the custom option in the toggle range
			optCount--
		}
		if optCount > 0 {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("  space/1-%d=toggle  enter=submit  esc=back", optCount)))
		} else {
			lines = append(lines, dimStyle.Render("  enter=submit  esc=back"))
		}
	}

	return lines
}

func (m *tuiModel) renderSessionRow(item listItem, idx, nameWidth, reasonWidth int) (string, string) {
	// Find the group for this session
	var group *sessionGroup
	for gi := range m.groups {
		if m.groups[gi].name == item.session {
			group = &m.groups[gi]
			break
		}
	}

	// Session icon: worst status across panes
	icon := dimStyle.Render("·")
	if group != nil {
		if group.blocked > 0 {
			icon = blockedStyle.Render("⚠")
		} else if group.active > 0 {
			icon = activeStyle.Render("✓")
		}
	}

	// Expand/collapse indicator
	arrow := "▶"
	if m.expanded[item.session] {
		arrow = "▼"
	}

	// Session summary in the reason column
	var reason string
	if group != nil {
		parts := []string{fmt.Sprintf("%d pane", len(group.verdicts))}
		if len(group.verdicts) != 1 {
			parts[0] += "s"
		}
		if group.blocked > 0 {
			parts = append(parts, fmt.Sprintf("%d blocked", group.blocked))
		}
		reason = strings.Join(parts, ", ")
	}

	var nameCol, reasonCol string
	if idx == m.cursor {
		nameCol = selectedStyle.Render(padRight(
			fmt.Sprintf("→ %s %s %s", arrow, sessionIcon(group), item.session), nameWidth))
		reasonCol = selectedStyle.Render(padRight(reason, reasonWidth))
	} else {
		nameCol = padRight(fmt.Sprintf("  %s %s %s", arrow, icon, item.session), nameWidth)
		reasonCol = dimStyle.Render(padRight(reason, reasonWidth))
	}

	return nameCol, reasonCol
}

func (m *tuiModel) renderPaneRow(item listItem, idx, nameWidth, reasonWidth int) (string, string) {
	v := m.verdicts[item.paneIdx]

	icon := activeStyle.Render("✓")
	if v.Blocked {
		icon = blockedStyle.Render("⚠")
	}
	if v.Agent == "error" {
		icon = errorStyle.Render("✗")
	}
	if v.Agent == "not_an_agent" {
		icon = dimStyle.Render("·")
	}

	// Show pane target (e.g. ":0.1") indented under the session
	paneLabel := fmt.Sprintf(":%d.%d", v.Window, v.Pane)

	// Sanitize reason: collapse newlines/tabs to spaces and truncate.
	// The LLM sometimes returns multi-line reasons or JSON fragments
	// which would break the row-based TUI layout.
	reason := strings.Join(strings.Fields(v.Reason), " ")
	reason = truncate(reason, reasonWidth-1)

	var nameCol, reasonCol string
	if idx == m.cursor {
		nameCol = selectedStyle.Render(padRight(
			fmt.Sprintf("→     %s %s", iconText(v), paneLabel), nameWidth))
		reasonCol = selectedStyle.Render(padRight(reason, reasonWidth))
	} else {
		nameCol = padRight(fmt.Sprintf("      %s %s", icon, paneLabel), nameWidth)
		reasonCol = padRight(reason, reasonWidth)
	}

	return nameCol, reasonCol
}

// sessionIcon returns an icon string for a session group.
func sessionIcon(g *sessionGroup) string {
	if g == nil {
		return "·"
	}
	if g.blocked > 0 {
		return "⚠"
	}
	if g.active > 0 {
		return "✓"
	}
	return "·"
}

func iconText(v model.Verdict) string {
	if v.Blocked {
		return "⚠"
	}
	if v.Agent == "error" {
		return "✗"
	}
	if v.Agent == "not_an_agent" {
		return "·"
	}
	return "✓"
}

// riskOrdinal maps risk levels to ordinal values for comparison.
func riskOrdinal(risk string) int {
	switch risk {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	default:
		return 0
	}
}

// riskWithinThreshold returns true if actionRisk is at or below maxRisk.
func riskWithinThreshold(actionRisk, maxRisk string) bool {
	return riskOrdinal(actionRisk) > 0 && riskOrdinal(actionRisk) <= riskOrdinal(maxRisk)
}

// nudgeTask describes a single auto-nudge action to perform asynchronously.
type nudgeTask struct {
	target string
	keys   string
	raw    bool
	label  string
}

// autoNudgeCmd returns a tea.Cmd that sends the recommended action for each
// blocked pane whose recommended action is within the configured risk
// threshold. The actual tmux send-keys calls (which include subprocess
// invocations and deliberate sleeps) run in a goroutine so they don't block
// the TUI Update loop.
func (m *tuiModel) autoNudgeCmd() tea.Cmd {
	if !m.autoNudge {
		return nil
	}

	// Collect nudge tasks and invalidate cache eagerly (cache is safe to
	// mutate here because Update runs on a single goroutine).
	var tasks []nudgeTask
	for _, v := range m.verdicts {
		if v.Agent == "not_an_agent" || v.Agent == "error" || !v.Blocked {
			continue
		}
		if len(v.Actions) == 0 || v.Recommended >= len(v.Actions) {
			continue
		}
		action := v.Actions[v.Recommended]
		if !riskWithinThreshold(action.Risk, m.autoNudgeMaxRisk) {
			continue
		}
		tasks = append(tasks, nudgeTask{
			target: v.Target,
			keys:   action.Keys,
			raw:    action.Raw,
			label:  action.Label,
		})
		// Invalidate cache so the next scan re-evaluates this pane
		if m.scanner.Cache != nil {
			m.scanner.Cache.Invalidate(v.Target)
			m.scanner.Metrics.RecordCacheInvalidation(m.ctx)
		}
	}

	if len(tasks) == 0 {
		return nil
	}

	return func() tea.Msg {
		var messages []string
		for _, t := range tasks {
			err := NudgePane(t.target, t.keys, t.raw)
			if err != nil {
				messages = append(messages, fmt.Sprintf("auto-nudge %s failed: %v", t.target, err))
			} else {
				messages = append(messages, fmt.Sprintf("auto-nudged '%s' to %s (%s)", t.keys, t.target, t.label))
			}
		}
		return nudgeResultMsg{messages: messages}
	}
}

// riskLabel returns a styled risk string.
func riskLabel(risk string) string {
	switch risk {
	case "low":
		return riskLowStyle.Render("low")
	case "medium":
		return riskMedStyle.Render("med")
	case "high":
		return riskHighStyle.Render("HIGH")
	default:
		return risk
	}
}

// jumpToPane switches the tmux client to the given pane target.
// The target can be a session name ("mysession"), or a full pane target
// ("mysession:0.1") to navigate to a specific window and pane.
// Returns an error message if navigation fails, empty string on success.
func jumpToPane(target string) string {
	cmd := exec.Command("tmux", "switch-client", "-t", target)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Sprintf("jump to %s failed: %v (%s)", target, err, strings.TrimSpace(string(out)))
	}
	return ""
}

// truncate cuts a string to at most maxLen runes (not bytes), appending "..."
// when truncation occurs. This is safe for multi-byte UTF-8 strings from LLM output.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

// wrapText wraps a string into lines of at most maxLen runes, breaking at spaces.
// Uses rune-aware slicing to avoid cutting multi-byte UTF-8 characters.
func wrapText(s string, maxLen int) []string {
	if maxLen <= 0 {
		return []string{s}
	}
	runes := []rune(s)
	var lines []string
	for len(runes) > 0 {
		if len(runes) <= maxLen {
			lines = append(lines, string(runes))
			break
		}
		// Find last space before maxLen
		cut := maxLen
		for i := maxLen - 1; i > 0; i-- {
			if runes[i] == ' ' {
				cut = i
				break
			}
		}
		lines = append(lines, string(runes[:cut]))
		// Skip leading spaces
		for cut < len(runes) && runes[cut] == ' ' {
			cut++
		}
		runes = runes[cut:]
	}
	return lines
}

// padRight pads a string with spaces to reach the desired visible width.
func padRight(s string, width int) string {
	visible := visibleLen(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
}

// visibleLen returns the visible length of a string, ignoring ANSI escape sequences.
func visibleLen(s string) int {
	n := 0
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		n++
	}
	return n
}
