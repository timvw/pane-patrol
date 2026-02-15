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
)

// view mode
type viewMode int

const (
	modeVerdictList viewMode = iota
	modeTextInput
)

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
	mode            viewMode
	focus           focusPanel

	// grouped list
	groups   []sessionGroup
	expanded map[string]bool // session name -> expanded
	items    []listItem      // visible items (rebuilt on verdicts/expand change)

	// action panel state
	actionCursor int // selected action index (0-based) in the right panel

	// text input state
	textInput  textinput.Model
	textTarget *model.Verdict // pane to send typed text to

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

	// cumulative token usage (incremented after each scan)
	totalInputTokens  int64
	totalOutputTokens int64
	totalCacheHits    int
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
// Non-agent panes (agent == "not_an_agent") are excluded — we only supervise
// AI coding agents.
func (m *tuiModel) rebuildGroups() {
	// Group verdicts by session, preserving order of first appearance.
	// Skip non-agent panes and non-blocked panes — we only show panes
	// that need human attention.
	seen := map[string]int{} // session -> index in groups
	m.groups = nil
	for i, v := range m.verdicts {
		if v.Agent == "not_an_agent" || v.Agent == "error" || !v.Blocked {
			continue
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
		if v.Agent != "error" && !v.Blocked {
			m.groups[idx].active++
		}
	}

	// Sort groups alphabetically for a stable, predictable order.
	// Blocked status is indicated by icons — no reordering on status change.
	sort.SliceStable(m.groups, func(i, j int) bool {
		return m.groups[i].name < m.groups[j].name
	})

	// Auto-expand single-pane sessions
	for _, g := range m.groups {
		if len(g.verdicts) == 1 {
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

// executeSelectedAction sends the currently highlighted action to the target pane,
// navigates tmux to that pane so the user can see the result, and triggers a rescan.
func (m *tuiModel) executeSelectedAction() tea.Cmd {
	v := m.selectedVerdict()
	if v == nil || !v.Blocked || m.actionCursor >= len(v.Actions) {
		return nil
	}
	action := v.Actions[m.actionCursor]
	err := NudgePane(v.Target, action.Keys)
	if err != nil {
		m.message = fmt.Sprintf("Nudge failed: %v", err)
	} else {
		m.message = fmt.Sprintf("Sent '%s' to %s (%s)", action.Keys, v.Target, action.Label)
	}
	// Invalidate cache so the next scan re-evaluates this pane
	if m.scanner.Cache != nil {
		m.scanner.Cache.Invalidate(v.Target)
	}
	// Navigate tmux to the target pane so the user sees the result
	jumpToPane(v.Target)
	m.focus = panelList
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
			// Accumulate token usage from this scan
			for _, v := range msg.result.Verdicts {
				m.totalInputTokens += v.Usage.InputTokens
				m.totalOutputTokens += v.Usage.OutputTokens
			}

			// Auto-nudge blocked panes (only when not focused on actions/typing)
			if m.focus != panelActions && m.mode != modeTextInput {
				if msgs := m.processAutoNudge(); len(msgs) > 0 {
					m.message = strings.Join(msgs, " | ")
				}
			}

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
		// Schedule next auto-refresh
		if cmd := m.scheduleTick(); cmd != nil {
			return m, cmd
		}
		return m, nil

	case tickMsg:
		// Auto-refresh: skip if already scanning, focused on actions, or typing
		if m.scanning || m.mode == modeTextInput || m.focus == panelActions {
			return m, m.scheduleTick()
		}
		m.scanning = true
		return m, m.doScan()
	}

	return m, nil
}

func (m *tuiModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeVerdictList:
		if m.focus == panelActions {
			return m.handleActionPanelKey(msg)
		}
		return m.handleVerdictListKey(msg)
	case modeTextInput:
		return m.handleTextInputKey(msg)
	}
	return m, nil
}

func (m *tuiModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeVerdictList {
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
		jumpToPane(m.verdicts[item.paneIdx].Target)
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
		jumpToPane(m.verdicts[item.paneIdx].Target)
		return m, nil

	case "right", "l", "tab":
		// Move focus to action panel if there are actions
		if m.selectedActionCount() > 0 {
			m.focus = panelActions
			v := m.selectedVerdict()
			if v != nil {
				m.actionCursor = v.Recommended
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
		// Open text input to type a response to the selected pane
		if m.cursor >= 0 && m.cursor < len(m.items) {
			v := m.selectedVerdict()
			if v != nil {
				m.mode = modeTextInput
				m.textTarget = v
				m.textInput.SetValue("")
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
		// Open text input from action panel
		v := m.selectedVerdict()
		if v != nil {
			m.mode = modeTextInput
			m.focus = panelList
			m.textTarget = v
			m.textInput.SetValue("")
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

func (m *tuiModel) handleTextInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "escape":
		m.mode = modeVerdictList
		m.textTarget = nil
		m.textInput.Blur()
		return m, nil

	case "enter":
		text := m.textInput.Value()
		if text != "" && m.textTarget != nil {
			target := m.textTarget.Target
			err := NudgePane(target, text)
			if err != nil {
				m.message = fmt.Sprintf("Send failed: %v", err)
			} else {
				m.message = fmt.Sprintf("Sent '%s' to %s", truncate(text, 40), target)
			}
			// Invalidate cache so the next scan re-evaluates this pane
			if m.scanner.Cache != nil {
				m.scanner.Cache.Invalidate(target)
			}
			// Navigate tmux to the target pane
			jumpToPane(target)
		}
		m.mode = modeVerdictList
		m.textTarget = nil
		m.textInput.Blur()
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

	switch m.mode {
	case modeVerdictList:
		return m.viewVerdictList()
	case modeTextInput:
		return m.viewTextInput()
	}
	return ""
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
		b.WriteString(dimStyle.Render(fmt.Sprintf("Enter/click=jump  →/Tab=actions  1-9=action  t=type  %s  r=rescan  q=quit", autoLabel)))
	}
	if m.totalInputTokens > 0 || m.totalOutputTokens > 0 {
		b.WriteString("  ")
		tokenInfo := fmt.Sprintf("tokens: %s in / %s out",
			formatTokens(m.totalInputTokens), formatTokens(m.totalOutputTokens))
		if m.totalCacheHits > 0 {
			tokenInfo += fmt.Sprintf(" | cache hits: %d", m.totalCacheHits)
		}
		b.WriteString(dimStyle.Render(tokenInfo))
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
	summary := fmt.Sprintf("  %d blocked | %d active | %d sessions | %d panes | scan #%d",
		totalBlocked, totalActive, len(m.groups), len(m.verdicts), m.scanCount)
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

	// Blocked: show the question/reason
	if v.Reason != "" {
		for _, rl := range wrapText(v.Reason, width) {
			lines = append(lines, blockedStyle.Render(rl))
		}
	}
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

	// Show hint for custom text input when focused
	if focused {
		lines = append(lines, "")
		lines = append(lines, dimStyle.Render("  t = type custom response"))
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
			fmt.Sprintf("→ %s %s %s", arrow, iconText2(group), item.session), nameWidth))
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

	reason := v.Reason
	if len(reason) > reasonWidth-1 {
		reason = reason[:reasonWidth-4] + "..."
	}

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

func (m *tuiModel) viewTextInput() string {
	if m.textTarget == nil {
		return ""
	}

	var b strings.Builder

	b.WriteString(titleStyle.Render("  Type Response"))
	b.WriteString("\n")
	b.WriteString(headerStyle.Render("  ─────────────────────────────────────────"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  Target: %s\n", m.textTarget.Target))
	b.WriteString(fmt.Sprintf("  Reason: %s\n", m.textTarget.Reason))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  Enter=send  Escape=cancel"))
	b.WriteString("\n\n")
	b.WriteString("  " + m.textInput.View())
	b.WriteString("\n")

	return b.String()
}

// iconText2 returns an icon string for a session group.
func iconText2(g *sessionGroup) string {
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

// processAutoNudge sends the recommended action for each blocked pane whose
// recommended action is within the configured risk threshold. Returns a list
// of status messages describing what was sent.
func (m *tuiModel) processAutoNudge() []string {
	if !m.autoNudge {
		return nil
	}

	var messages []string
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
		err := NudgePane(v.Target, action.Keys)
		if err != nil {
			messages = append(messages, fmt.Sprintf("auto-nudge %s failed: %v", v.Target, err))
		} else {
			messages = append(messages, fmt.Sprintf("auto-nudged '%s' to %s (%s)", action.Keys, v.Target, action.Label))
		}
		// Invalidate cache so the next scan re-evaluates this pane
		if m.scanner.Cache != nil {
			m.scanner.Cache.Invalidate(v.Target)
		}
	}
	return messages
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
func jumpToPane(target string) {
	cmd := exec.Command("tmux", "switch-client", "-t", target)
	_ = cmd.Run()
}

// truncate cuts a string to at most maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// wrapText wraps a string into lines of at most maxLen characters, breaking at spaces.
func wrapText(s string, maxLen int) []string {
	if maxLen <= 0 {
		return []string{s}
	}
	var lines []string
	for len(s) > 0 {
		if len(s) <= maxLen {
			lines = append(lines, s)
			break
		}
		// Find last space before maxLen
		cut := maxLen
		if idx := strings.LastIndex(s[:maxLen], " "); idx > 0 {
			cut = idx
		}
		lines = append(lines, s[:cut])
		s = strings.TrimLeft(s[cut:], " ")
	}
	return lines
}

// formatTokens formats a token count for display (e.g., "12.3k").
func formatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 10000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	if n < 1000000 {
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1000000)
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
