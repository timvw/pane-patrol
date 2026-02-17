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

// Styles are stored in tuiModel.s (built from the configurable Theme).
// See theme.go for Theme struct and DarkTheme()/LightTheme() constructors.

// panelClickKind describes what happens when an action panel row is clicked.
type panelClickKind int

const (
	clickNone   panelClickKind = iota // non-clickable (headers, text, separators)
	clickAction                       // execute v.Actions[index]
	clickToggle                       // toggle local checkbox for v.Actions[index]
	clickTabBar                       // tab bar row: check X position against tabZones
)

// panelClickInfo maps an action panel row to its click behavior.
type panelClickInfo struct {
	kind  panelClickKind
	index int // action index or -1
}

// tabClickZone maps an X range on the tab bar to a specific tab.
type tabClickZone struct {
	xStart, xEnd int // visible character range (0-indexed, relative to panel line)
	tabIndex     int
}

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
	panelList    focusPanel = iota // top: session/pane list
	panelActions                   // bottom: action panel
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
	ThemeName        string        // "dark" (default) or "light"
}

// model implements tea.Model
type tuiModel struct {
	theme Theme
	s     styles // derived from theme

	scanner         *Scanner
	ctx             context.Context
	refreshInterval time.Duration
	verdicts        []model.Verdict
	cursor          int
	focus           focusPanel

	// display filter
	filter displayFilter

	// grouped list
	groups          []sessionGroup
	expanded        map[string]bool // session name -> expanded
	manualCollapsed map[string]bool // sessions the user explicitly collapsed (immune to auto-expand)
	items           []listItem      // visible items (rebuilt on verdicts/expand change)

	// action panel state
	actionCursor int  // selected action index (0-based) in the right panel
	editing      bool // true when inline text input is active in the action panel

	// text input state (rendered inline in the action panel)
	textInput textinput.Model

	// Buffered selection state for question dialogs.
	// All toggles and text input are accumulated locally and only sent to
	// the target pane on explicit Submit. This allows the user to review
	// and correct their selections before committing.
	localChecks   map[int]bool // desired checked state per option index (multi-select)
	initialChecks map[int]bool // initial state from parser (for computing diff on submit)
	localTarget   string       // pane ID the buffered state belongs to (reset on switch)

	// layout (computed in viewVerdictList, used for mouse hit testing)
	actionPanelY int // Y offset where the action panel starts
	listStart    int // scroll offset for list (for mouse hit testing)

	// Click target map for the action panel (populated during rendering).
	// Index = line offset within the action panel; value = click behavior.
	panelClicks []panelClickInfo
	tabZones    []tabClickZone // X-position zones for the tab bar row

	// Mouse hover state
	hoverPanelRow int // -1 = no hover; row index in panelClicks for highlight

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
	theme := ThemeByName(t.ThemeName)
	s := newStyles(theme)

	ti := textinput.New()
	ti.Placeholder = "Type response and press Enter..."
	ti.CharLimit = 2048
	ti.Width = 80
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(theme.Primary)
	ti.TextStyle = lipgloss.NewStyle().Foreground(theme.Text)
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(theme.TextMuted)

	maxRisk := t.AutoNudgeMaxRisk
	if maxRisk == "" {
		maxRisk = "low"
	}

	m := &tuiModel{
		theme:            theme,
		s:                s,
		scanner:          t.Scanner,
		ctx:              ctx,
		refreshInterval:  t.RefreshInterval,
		expanded:         make(map[string]bool),
		manualCollapsed:  make(map[string]bool),
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
	m.hoverPanelRow = -1
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
	// Respect manual collapses: if the user explicitly collapsed a session,
	// don't auto-expand it until the user re-expands it manually.
	for _, g := range m.groups {
		if m.manualCollapsed[g.name] {
			continue
		}
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

// submitBufferedSelection compiles the buffered local selection state into a
// sequence of keystrokes and sends them to the target pane in one batch.
// For each option whose checked state changed from the initial parser state,
// it sends the toggle key. If the custom answer option is checked and has
// text, it sends the activation key + text + Enter. Finally it sends Enter
// to submit the selection.
func (m *tuiModel) submitBufferedSelection() tea.Cmd {
	v := m.selectedVerdict()
	if v == nil {
		return nil
	}
	target := v.Target

	// Compute toggle diff: send keys only for options that changed.
	// Sort by index to send in order (deterministic, matches agent's UI).
	var toggleIdxs []int
	for i, desired := range m.localChecks {
		if desired != m.initialChecks[i] {
			toggleIdxs = append(toggleIdxs, i)
		}
	}
	sort.Ints(toggleIdxs)

	// Find custom answer option index (if any)
	customIdx := -1
	for i := range v.Actions {
		if isCustomAnswerAction(v.Actions[i]) {
			customIdx = i
			break
		}
	}

	customText := strings.TrimSpace(m.textInput.Value())

	// Send toggle keys for regular options (not the custom answer)
	for _, idx := range toggleIdxs {
		if idx == customIdx {
			continue // handle custom answer separately below
		}
		if idx < len(v.Actions) {
			if err := NudgePane(target, v.Actions[idx].Keys, v.Actions[idx].Raw); err != nil {
				m.message = fmt.Sprintf("Toggle option %d failed: %v", idx+1, err)
				// Continue with remaining toggles
			}
		}
	}

	// Send custom answer if checked and has text
	if customIdx >= 0 && m.localChecks[customIdx] && customText != "" {
		a := v.Actions[customIdx]
		if err := NudgePane(target, a.Keys, a.Raw); err != nil {
			m.message = fmt.Sprintf("Custom answer activate failed: %v", err)
		} else if err := NudgePane(target, customText, false); err != nil {
			m.message = fmt.Sprintf("Custom answer text failed: %v", err)
		} else if err := NudgePane(target, "Enter", true); err != nil {
			m.message = fmt.Sprintf("Custom answer confirm failed: %v", err)
		}
	} else if customIdx >= 0 && m.localChecks[customIdx] != m.initialChecks[customIdx] {
		// Custom option toggled but no text — just toggle the checkbox
		a := v.Actions[customIdx]
		if err := NudgePane(target, a.Keys, a.Raw); err != nil {
			m.message = fmt.Sprintf("Toggle custom option failed: %v", err)
		}
	}

	// Send Enter to submit the selection
	if err := NudgePane(target, "Enter", true); err != nil {
		m.message = fmt.Sprintf("Submit failed: %v", err)
	} else if m.message == "" {
		// Build summary of selections
		var selected []string
		for i := 0; i < len(v.Actions); i++ {
			if m.localChecks[i] && isToggleAction(v.Actions[i]) {
				selected = append(selected, fmt.Sprintf("%d", i+1))
			}
		}
		summary := strings.Join(selected, ",")
		if customText != "" {
			summary += "+text"
		}
		m.message = fmt.Sprintf("Submitted [%s] to %s", summary, target)
	}

	// Clean up
	if m.scanner != nil && m.scanner.Cache != nil {
		m.scanner.Cache.Invalidate(target)
		m.scanner.Metrics.RecordCacheInvalidation(m.ctx)
	}
	m.editing = false
	m.textInput.Blur()
	m.textInput.SetValue("")
	m.resetLocalChecks()
	m.focus = panelList
	m.scanning = true
	if m.scanner != nil {
		return m.doScan()
	}
	return nil
}

// navigateTab sends a tab navigation key ("Tab" or "BTab") to the target pane.
// For multi-select questions, buffered toggle selections are sent first.
// After sending, it invalidates the cache and triggers a rescan.
func (m *tuiModel) navigateTab(key string) tea.Cmd {
	v := m.selectedVerdict()
	if v == nil {
		return nil
	}

	// Multi-select: submit buffered toggles + send the navigation key.
	if isMultiSelectQuestion(v) {
		return m.submitBufferedAndSendKey(key)
	}

	// Single-select or confirm tab: send the key directly.
	if err := NudgePane(v.Target, key, true); err != nil {
		m.message = fmt.Sprintf("Send %s failed: %v", key, err)
	} else {
		dir := "next"
		if key == "BTab" {
			dir = "prev"
		}
		m.message = fmt.Sprintf("%s tab on %s", dir, v.Target)
	}
	if m.scanner != nil && m.scanner.Cache != nil {
		m.scanner.Cache.Invalidate(v.Target)
		m.scanner.Metrics.RecordCacheInvalidation(m.ctx)
	}
	m.editing = false
	m.textInput.Blur()
	m.textInput.SetValue("")
	m.resetLocalChecks()
	// Keep focus on the action panel: the user is navigating tabs, so
	// they want to stay in the action panel to interact with the next tab.
	m.focus = panelActions
	m.actionCursor = 0
	m.scanning = true
	if m.scanner != nil {
		return m.doScan()
	}
	return nil
}

// clickTab navigates to a specific tab by index. It computes the number of
// Tab or BTab presses needed to reach the target from the current active tab,
// then sends them. For multi-select questions, buffered selections are submitted
// before the first navigation key.
func (m *tuiModel) clickTab(v *model.Verdict, targetTab int) tea.Cmd {
	// Determine the current active tab (same logic as renderTabBar).
	tabs := parseTabsFromWaitingFor(v.WaitingFor)
	if len(tabs) == 0 {
		return nil
	}
	activeTab := 0
	if isConfirmTab(v) {
		activeTab = len(tabs) - 1
	}

	delta := targetTab - activeTab
	if delta == 0 {
		return nil // Already on the target tab
	}

	// Choose direction and key
	key := "Tab"
	steps := delta
	if delta < 0 {
		key = "BTab"
		steps = -delta
	}

	// For a single step, delegate to navigateTab which handles buffered
	// selections and rescanning.
	if steps == 1 {
		return m.navigateTab(key)
	}

	// Multi-step: submit buffered state once, then send multiple keys.
	if isMultiSelectQuestion(v) {
		m.initLocalChecks(v)
		// Submit buffered toggles and the first navigation key
		cmd := m.submitBufferedAndSendKey(key)
		// Send remaining navigation keys directly
		for i := 1; i < steps; i++ {
			if err := NudgePane(v.Target, key, true); err != nil {
				m.message = fmt.Sprintf("Tab navigation step %d failed: %v", i+1, err)
				break
			}
		}
		return cmd
	}

	// Non-multi-select: send all keys directly
	for i := 0; i < steps; i++ {
		if err := NudgePane(v.Target, key, true); err != nil {
			m.message = fmt.Sprintf("Tab navigation step %d failed: %v", i+1, err)
			break
		}
	}
	dir := "forward"
	if key == "BTab" {
		dir = "backward"
	}
	m.message = fmt.Sprintf("Navigated %d tabs %s on %s", steps, dir, v.Target)

	if m.scanner != nil && m.scanner.Cache != nil {
		m.scanner.Cache.Invalidate(v.Target)
		m.scanner.Metrics.RecordCacheInvalidation(m.ctx)
	}
	m.editing = false
	m.textInput.Blur()
	m.textInput.SetValue("")
	m.resetLocalChecks()
	m.focus = panelActions
	m.actionCursor = 0
	m.scanning = true
	if m.scanner != nil {
		return m.doScan()
	}
	return nil
}

// submitBufferedAndSendKey compiles the buffered local selection state into a
// sequence of keystrokes (like submitBufferedSelection), but instead of sending
// Enter at the end, sends the specified key. Used for Tab navigation in
// multi-tab forms: save current tab's selections, then advance to the next tab.
func (m *tuiModel) submitBufferedAndSendKey(key string) tea.Cmd {
	v := m.selectedVerdict()
	if v == nil {
		return nil
	}
	target := v.Target

	// Compute toggle diff: send keys only for options that changed.
	var toggleIdxs []int
	for i, desired := range m.localChecks {
		if desired != m.initialChecks[i] {
			toggleIdxs = append(toggleIdxs, i)
		}
	}
	sort.Ints(toggleIdxs)

	// Find custom answer option index (if any)
	customIdx := -1
	for i := range v.Actions {
		if isCustomAnswerAction(v.Actions[i]) {
			customIdx = i
			break
		}
	}

	customText := strings.TrimSpace(m.textInput.Value())

	// Send toggle keys for regular options (not the custom answer)
	for _, idx := range toggleIdxs {
		if idx == customIdx {
			continue
		}
		if idx < len(v.Actions) {
			if err := NudgePane(target, v.Actions[idx].Keys, v.Actions[idx].Raw); err != nil {
				m.message = fmt.Sprintf("Toggle option %d failed: %v", idx+1, err)
			}
		}
	}

	// Send custom answer if checked and has text
	if customIdx >= 0 && m.localChecks[customIdx] && customText != "" {
		a := v.Actions[customIdx]
		if err := NudgePane(target, a.Keys, a.Raw); err != nil {
			m.message = fmt.Sprintf("Custom answer activate failed: %v", err)
		} else if err := NudgePane(target, customText, false); err != nil {
			m.message = fmt.Sprintf("Custom answer text failed: %v", err)
		} else if err := NudgePane(target, "Enter", true); err != nil {
			m.message = fmt.Sprintf("Custom answer confirm failed: %v", err)
		}
	} else if customIdx >= 0 && m.localChecks[customIdx] != m.initialChecks[customIdx] {
		a := v.Actions[customIdx]
		if err := NudgePane(target, a.Keys, a.Raw); err != nil {
			m.message = fmt.Sprintf("Toggle custom option failed: %v", err)
		}
	}

	// Send the specified key (e.g., Tab) instead of Enter
	if err := NudgePane(target, key, true); err != nil {
		m.message = fmt.Sprintf("Send %s failed: %v", key, err)
	} else if m.message == "" {
		m.message = fmt.Sprintf("Sent selections + %s to %s", key, target)
	}

	// Clean up
	if m.scanner != nil && m.scanner.Cache != nil {
		m.scanner.Cache.Invalidate(target)
		m.scanner.Metrics.RecordCacheInvalidation(m.ctx)
	}
	m.editing = false
	m.textInput.Blur()
	m.textInput.SetValue("")
	m.resetLocalChecks()
	m.focus = panelList
	m.scanning = true
	if m.scanner != nil {
		return m.doScan()
	}
	return nil
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
	if m.scanner != nil && m.scanner.Cache != nil {
		m.scanner.Cache.Invalidate(v.Target)
		m.scanner.Metrics.RecordCacheInvalidation(m.ctx)
	}
	m.resetLocalChecks()
	m.focus = panelList
	m.scanning = true
	if m.scanner != nil {
		return m.doScan()
	}
	return nil
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

	// Hover: move cursor to hovered item (like OpenCode's onMouseOver).
	if msg.Action == tea.MouseActionMotion {
		m.hoverPanelRow = -1
		if m.actionPanelY > 0 && msg.Y >= m.actionPanelY {
			// Hovering action panel
			row := msg.Y - m.actionPanelY
			if row >= 0 && row < len(m.panelClicks) {
				info := m.panelClicks[row]
				if info.kind == clickAction || info.kind == clickToggle {
					m.hoverPanelRow = row
					m.actionCursor = info.index
					m.focus = panelActions
				}
			}
		} else {
			// Hovering list panel
			idx := msg.Y - 1 + m.listStart
			if idx >= 0 && idx < len(m.items) {
				m.cursor = idx
			}
		}
		return m, nil
	}

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	// Determine if click is in the action panel (bottom section)
	if m.actionPanelY > 0 && msg.Y >= m.actionPanelY {
		return m.handleActionPanelClick(msg)
	}

	// Click in the list panel: header line is row 0, items start at row 1
	clickedIdx := msg.Y - 1 + m.listStart // offset for header line + scroll
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
		if m.expanded[item.session] {
			delete(m.manualCollapsed, item.session)
		} else {
			m.manualCollapsed[item.session] = true
		}
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

	// Use the click target tracking populated during rendering.
	clickedPanelRow := msg.Y - m.actionPanelY
	if clickedPanelRow < 0 || clickedPanelRow >= len(m.panelClicks) {
		// Click outside the rendered panel — just focus it
		m.focus = panelActions
		m.clampActionCursor()
		return m, nil
	}

	info := m.panelClicks[clickedPanelRow]
	switch info.kind {
	case clickAction:
		if info.index < 0 || info.index >= len(v.Actions) {
			break
		}
		m.actionCursor = info.index
		m.focus = panelActions
		// Multi-select submit
		if isMultiSelectQuestion(v) && v.Actions[info.index].Label == "submit selection" {
			return m, m.submitBufferedSelection()
		}
		return m, m.executeSelectedAction()

	case clickToggle:
		if info.index < 0 || info.index >= len(v.Actions) {
			break
		}
		m.actionCursor = info.index
		m.focus = panelActions
		m.initLocalChecks(v)
		m.localChecks[info.index] = !m.localChecks[info.index]
		return m, nil

	case clickTabBar:
		// Look up which tab was clicked based on X position
		for _, zone := range m.tabZones {
			if msg.X >= zone.xStart && msg.X < zone.xEnd {
				m.focus = panelActions
				return m, m.clickTab(v, zone.tabIndex)
			}
		}
		// Clicked on tab bar but not on a specific tab
		m.focus = panelActions
		m.clampActionCursor()
		return m, nil

	case clickNone:
		// Non-clickable area — just focus the action panel
		m.focus = panelActions
		m.clampActionCursor()
		return m, nil
	}

	m.focus = panelActions
	m.clampActionCursor()
	return m, nil
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
			m.resetLocalChecks()
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
			m.resetLocalChecks()
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
			if m.expanded[item.session] {
				delete(m.manualCollapsed, item.session)
			} else {
				m.manualCollapsed[item.session] = true
			}
			m.rebuildItems()
			if m.expanded[item.session] && m.cursor+1 < len(m.items) {
				m.cursor++
			}
			return m, nil
		}
		// Pane item: switch tmux client to this pane
		if errMsg := jumpToPane(m.verdicts[item.paneIdx].Target); errMsg != "" {
			m.message = errMsg
		}
		return m, nil

	case "right", "l":
		if m.cursor < 0 || m.cursor >= len(m.items) {
			return m, nil
		}
		item := m.items[m.cursor]
		if item.kind == itemSession {
			// Expand session and move to first pane
			if !m.expanded[item.session] {
				m.expanded[item.session] = true
				delete(m.manualCollapsed, item.session)
				m.rebuildItems()
			}
			if m.cursor+1 < len(m.items) {
				m.cursor++
			}
			return m, nil
		}
		// Pane item: focus the action panel to interact with actions.
		if m.selectedActionCount() > 0 {
			m.focus = panelActions
			v := m.selectedVerdict()
			if v != nil {
				m.actionCursor = v.Recommended
				if isMultiSelectQuestion(v) {
					m.initLocalChecks(v)
				}
			}
			m.clampActionCursor()
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
			m.manualCollapsed[item.session] = true
			m.rebuildItems()
			if m.cursor >= len(m.items) {
				m.cursor = len(m.items) - 1
			}
			return m, nil
		}

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// Quick-execute the Nth action for the currently selected pane.
		// For multi-select questions, move focus to action panel and toggle locally.
		v := m.selectedVerdict()
		if v == nil || !v.Blocked {
			return m, nil
		}
		idx := int(msg.String()[0] - '1') // 0-based
		if idx >= len(v.Actions) {
			return m, nil
		}
		m.actionCursor = idx
		if isMultiSelectQuestion(v) && isToggleAction(v.Actions[idx]) {
			m.focus = panelActions
			m.initLocalChecks(v)
			m.localChecks[idx] = !m.localChecks[idx]
			return m, nil
		}
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

	case "esc", "escape", "left":
		// Return focus to the list panel, discard buffered selections.
		m.resetLocalChecks()
		m.focus = panelList
		return m, nil

	case "up", "k":
		count := m.selectedActionCount()
		if count > 0 {
			m.actionCursor = (m.actionCursor - 1 + count) % count
		}

	case "down", "j":
		count := m.selectedActionCount()
		if count > 0 {
			m.actionCursor = (m.actionCursor + 1) % count
		}

	case "enter":
		v := m.selectedVerdict()
		if v != nil && isMultiSelectQuestion(v) {
			// Multi-select: Enter on a toggle action toggles locally;
			// Enter on Submit compiles and sends the buffered selection.
			if m.actionCursor < len(v.Actions) && isToggleAction(v.Actions[m.actionCursor]) {
				m.localChecks[m.actionCursor] = !m.localChecks[m.actionCursor]
				return m, nil
			}
			// Submit or Dismiss — submit sends buffered diff
			if m.actionCursor < len(v.Actions) && v.Actions[m.actionCursor].Label == "submit selection" {
				return m, m.submitBufferedSelection()
			}
		}
		return m, m.executeSelectedAction()

	case " ":
		// Spacebar toggles the current item locally in multi-select checklists.
		v := m.selectedVerdict()
		if v != nil && isMultiSelectQuestion(v) && m.actionCursor < len(v.Actions) {
			if isToggleAction(v.Actions[m.actionCursor]) {
				m.localChecks[m.actionCursor] = !m.localChecks[m.actionCursor]
				return m, nil
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
		// Multi-select questions: toggle locally, don't send to pane
		if isMultiSelectQuestion(v) && isToggleAction(v.Actions[idx]) {
			m.localChecks[idx] = !m.localChecks[idx]
			return m, nil
		}
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

	case "tab", "l":
		// Tab / l: advance to next tab in multi-tab forms.
		v := m.selectedVerdict()
		if v != nil && hasTabNavigation(v) {
			return m, m.navigateTab("Tab")
		}
		return m, nil

	case "shift+tab", "h":
		// Shift+Tab / h: go to previous tab in multi-tab forms.
		v := m.selectedVerdict()
		if v != nil && hasTabNavigation(v) {
			return m, m.navigateTab("BTab")
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
// certain keys are intercepted:
//   - Number keys 1-9: toggle local checkboxes (multi-select) or move cursor
//   - Space: toggle current checkbox locally (multi-select)
//   - Enter: submit buffered selection (multi-select) or text (single-select)
//   - Tab/Shift+Tab: navigate question tabs in multi-tab forms
//   - Escape: discard changes and return to list
//
// Arrow keys and all other keys are forwarded to the text input widget.
// All toggles are buffered locally and only sent to the target pane on Enter.
func (m *tuiModel) handleInlineTextInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit

	case "esc", "escape":
		m.editing = false
		m.textInput.Blur()
		m.textInput.SetValue("")
		m.resetLocalChecks()
		m.focus = panelList
		return m, nil

	case "q":
		// When text input is empty, q quits the app (consistent with other panels).
		// When text is present, fall through to forward the keystroke to the input.
		if m.textInput.Value() == "" {
			return m, tea.Quit
		}

	case " ":
		// Space toggles the current checkbox locally in multi-select
		v := m.selectedVerdict()
		if v != nil && isMultiSelectQuestion(v) && m.actionCursor < len(v.Actions) {
			if isToggleAction(v.Actions[m.actionCursor]) {
				m.localChecks[m.actionCursor] = !m.localChecks[m.actionCursor]
				return m, nil
			}
		}
		// Otherwise forward space to text input
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// Number keys toggle checkboxes locally (multi-select) or
		// move cursor (single-select). Never send to pane directly.
		v := m.selectedVerdict()
		if v != nil && v.Blocked {
			idx := int(msg.String()[0] - '1')
			if idx < len(v.Actions) {
				m.actionCursor = idx
				if isMultiSelectQuestion(v) && isToggleAction(v.Actions[idx]) {
					m.localChecks[idx] = !m.localChecks[idx]
				}
				// Single-select: just move cursor, don't send
			}
		}
		return m, nil

	case "tab":
		// Tab in text input: advance to next tab in multi-tab forms.
		// (left/right are NOT intercepted — they move the text cursor.)
		v := m.selectedVerdict()
		if v != nil && hasTabNavigation(v) {
			return m, m.navigateTab("Tab")
		}
		// Not a multi-tab form: forward tab to text input
		var tabCmd tea.Cmd
		m.textInput, tabCmd = m.textInput.Update(msg)
		return m, tabCmd

	case "shift+tab":
		// Shift+Tab in text input: go to previous tab in multi-tab forms.
		v := m.selectedVerdict()
		if v != nil && hasTabNavigation(v) {
			return m, m.navigateTab("BTab")
		}
		return m, nil

	case "enter":
		v := m.selectedVerdict()
		// Multi-select: Enter submits the buffered selection
		if v != nil && isMultiSelectQuestion(v) {
			return m, m.submitBufferedSelection()
		}
		// Single-select / custom answer: send text if present
		text := m.textInput.Value()
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
			if m.scanner != nil && m.scanner.Cache != nil {
				m.scanner.Cache.Invalidate(target)
				m.scanner.Metrics.RecordCacheInvalidation(m.ctx)
			}
		}
		m.editing = false
		m.textInput.Blur()
		m.textInput.SetValue("")
		m.resetLocalChecks()
		// Keep focus on the action panel: the user is navigating tabs, so
		// they want to stay in the action panel to interact with the next tab.
		m.focus = panelActions
		m.actionCursor = 0
		m.scanning = true
		if m.scanner != nil {
			return m, m.doScan()
		}
		return m, nil
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
	b.WriteString(m.s.title.Render("Pane Supervisor"))
	b.WriteString("  ")
	if m.focus == panelActions {
		b.WriteString(m.styleHeaderHints("↑↓=select  enter=execute  space=toggle  t=type  tab/s-tab=tabs  ←/esc=back  q=quit"))
	} else {
		autoLabel := "a=auto:OFF"
		if m.autoNudge {
			autoLabel = fmt.Sprintf("a=auto:ON(%s)", m.autoNudgeMaxRisk)
		}
		filterLabel := fmt.Sprintf("f=%s", m.filter)
		b.WriteString(m.styleHeaderHints(fmt.Sprintf("↑↓=nav  enter=jump  →/l=actions  1-9=quick  %s  %s  r=rescan  q=quit", filterLabel, autoLabel)))
	}
	if m.totalCacheHits > 0 {
		b.WriteString("  ")
		b.WriteString(m.s.dim.Render(fmt.Sprintf("eval cache: %d", m.totalCacheHits)))
	}
	if m.scanning {
		b.WriteString("  ")
		b.WriteString(m.s.blocked.Render("scanning..."))
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

	// Layout: 2-column list (name | reason) on top, action panel below
	nameWidth := 10
	for _, g := range m.groups {
		if len(g.name)+6 > nameWidth {
			nameWidth = len(g.name) + 6
		}
	}
	nameWidth += 6 // icon + indent + cursor + padding

	separator := " | "
	sepWidth := len(separator)

	// Reason gets all remaining width (no action column)
	reasonWidth := m.width - nameWidth - sepWidth
	if reasonWidth < 15 {
		reasonWidth = 15
	}

	// Build action panel content (full terminal width with margin)
	actionPanelWidth := m.width - 2
	if actionPanelWidth < 20 {
		actionPanelWidth = 20
	}
	actionLines := m.buildActionPanel(actionPanelWidth)

	// Height budget: header(1) + list + separator(1) + action panel + summary(1) + hints(1) + status(0-1)
	overhead := 4 // header + separator + summary + hints
	if m.message != "" {
		overhead++
	}
	available := m.height - overhead
	if available < 6 {
		available = 6
	}

	// Layout: give each panel what it needs; scroll/truncate only when space is tight
	totalActionLines := len(actionLines)
	totalItems := len(m.items)
	var listHeight, actionHeight int

	if totalItems+totalActionLines <= available {
		// Everything fits — no scrolling, no padding
		listHeight = totalItems
		actionHeight = totalActionLines
	} else {
		// Tight on space: action gets what it needs (up to half), list gets the rest
		actionHeight = totalActionLines
		maxActionHeight := available / 2
		if maxActionHeight < 6 {
			maxActionHeight = 6
		}
		if actionHeight > maxActionHeight {
			actionHeight = maxActionHeight
		}
		if actionHeight < 1 {
			actionHeight = 1
		}
		listHeight = available - actionHeight
		if listHeight < 3 {
			listHeight = 3
		}
	}

	// Count totals
	totalBlocked := 0
	totalActive := 0
	for _, g := range m.groups {
		totalBlocked += g.blocked
		totalActive += g.active
	}

	// Calculate visible window for scrolling
	maxVisible := listHeight
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

	// Render list rows (2 columns: name | reason — no action column)
	sep := m.s.header.Render(separator)
	listRowsRendered := 0
	for i := start; i < end && i < len(m.items); i++ {
		item := m.items[i]
		var nameCol, reasonCol string

		if item.kind == itemSession {
			nameCol, reasonCol = m.renderSessionRow(item, i, nameWidth, reasonWidth)
		} else {
			nameCol, reasonCol = m.renderPaneRow(item, i, nameWidth, reasonWidth)
		}

		b.WriteString(nameCol)
		b.WriteString(sep)
		b.WriteString(reasonCol)
		b.WriteString("\n")
		listRowsRendered++
	}

	// Pad remaining list rows to keep action panel position stable
	for listRowsRendered < listHeight {
		b.WriteString("\n")
		listRowsRendered++
	}

	// Separator between list and action panel
	b.WriteString(m.s.header.Render(strings.Repeat("─", m.width)))
	b.WriteString("\n")

	// Store layout offsets for mouse hit testing
	m.actionPanelY = 1 + listHeight + 1 // header + list rows + separator
	m.listStart = start                 // scroll offset

	// Action panel (full width, indented 1 space)
	for i := 0; i < actionHeight && i < len(actionLines); i++ {
		b.WriteString(" ")
		b.WriteString(actionLines[i])
		b.WriteString("\n")
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
	b.WriteString(m.s.dim.Render(summary))
	b.WriteString("\n")

	// Navigation hints (context-dependent) — already styled per-segment
	b.WriteString(m.buildHints())
	b.WriteString("\n")

	// Status message
	if m.message != "" {
		b.WriteString(m.s.status.Render("  " + m.message))
		b.WriteString("\n")
	}

	return b.String()
}

// buildHints returns a context-dependent keybinding hint line.
func (m *tuiModel) buildHints() string {
	if m.editing {
		hint := "  enter submit  esc cancel"
		v := m.selectedVerdict()
		if v != nil && hasTabNavigation(v) {
			hint += "  tab/s-tab tabs"
		}
		return m.styleHints(hint)
	}
	if m.focus == panelActions {
		hint := "  ↑↓ navigate  enter execute  ←/esc back  t type"
		v := m.selectedVerdict()
		if v != nil && hasTabNavigation(v) {
			hint += "  tab/s-tab tabs"
		}
		return m.styleHints(hint)
	}
	// List panel
	return m.styleHints("  ↑↓ navigate  enter jump  →/l actions  1-9 quick  r rescan  f filter  q quit")
}

// styleHints renders a hint string with key symbols in text color and
// descriptions in muted color. Hint format: "  key desc  key desc  ..."
// Each pair is separated by double spaces; the key is the first word, desc is the second.
func (m *tuiModel) styleHints(raw string) string {
	// Split on double-space to get "key desc" pairs (first entry is empty from leading spaces)
	pairs := strings.Split(raw, "  ")
	var b strings.Builder
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			b.WriteString("  ")
			continue
		}
		// Split into key and description
		parts := strings.SplitN(pair, " ", 2)
		b.WriteString("  ")
		b.WriteString(m.s.hintKey.Render(parts[0]))
		if len(parts) > 1 {
			b.WriteString(" ")
			b.WriteString(m.s.hintDesc.Render(parts[1]))
		}
	}
	return b.String()
}

// styleHeaderHints renders header hints with key=value format.
// Keys (before =) are in text color, values (after =) in muted.
// Pairs are separated by double spaces.
func (m *tuiModel) styleHeaderHints(raw string) string {
	pairs := strings.Split(raw, "  ")
	var b strings.Builder
	for i, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		if i > 0 {
			b.WriteString("  ")
		}
		if eqIdx := strings.Index(pair, "="); eqIdx >= 0 {
			b.WriteString(m.s.hintKey.Render(pair[:eqIdx]))
			b.WriteString(m.s.hintDesc.Render("=" + pair[eqIdx+1:]))
		} else {
			b.WriteString(m.s.hintDesc.Render(pair))
		}
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

	// Reset click target tracking for this render cycle.
	m.panelClicks = nil
	m.tabZones = nil

	var lines []string

	// Target header
	lines = m.addPanelLine(lines, m.s.dim.Render(truncate(v.Target, width)), clickNone, -1)

	if !v.Blocked {
		lines = m.addPanelLine(lines, m.s.active.Render("Active"), clickNone, -1)
		if v.Reason != "" {
			lines = m.addPanelLines(lines, wrapText(v.Reason, width))
		}
		return lines
	}

	// Multi-select questions: render interactive checklist instead of action buttons.
	if isMultiSelectQuestion(v) {
		return m.buildMultiSelectPanel(v, width, lines)
	}

	// Confirm tab: render tab bar + review content + submit/dismiss actions.
	if isConfirmTab(v) {
		return m.buildConfirmTabPanel(v, width, lines)
	}

	// Render tab bar for single-select tabs in multi-tab forms.
	if hasTabNavigation(v) {
		lines = m.renderTabBar(v, width, lines)
	}

	// Question dialogs: unified layout — question text + interactive options.
	// Non-question dialogs: dialog preview + action buttons.
	if isQuestionReason(v) {
		return m.buildQuestionPanel(v, width, lines)
	}

	// Non-question blocked: render agent-specific dialog representation
	lines = m.addPanelLines(lines, renderDialogContent(v, width, m.s))
	lines = m.addPanelLine(lines, "", clickNone, -1) // blank separator

	// Actions with number keys — highlight selected when panel is focused
	focused := m.focus == panelActions
	for i, a := range v.Actions {
		if i >= 9 {
			break
		}

		// Skip Tab/BTab actions from the numbered list — they're handled by
		// the tab bar and the Tab key binding, not as clickable actions.
		if a.Keys == "Tab" || a.Keys == "BTab" {
			continue
		}

		rec := " "
		if i == v.Recommended {
			rec = "*"
		}

		riskStr := m.riskLabel(a.Risk)

		// OpenCode layout: number separate from label, bg highlight for active.
		num := fmt.Sprintf(" %s%d.", rec, i+1)
		labelText := fmt.Sprintf(" %s %s (%s)", a.Keys, a.Label, riskStr)
		combined := truncate(num+labelText, width)
		if len(combined) > len(num) {
			labelText = combined[len(num):]
		}

		if focused && i == m.actionCursor {
			lines = m.addPanelLine(lines,
				m.s.optionActive.Render(padRight(num, len([]rune(num))+1))+
					m.s.optionBg.Render(padRight(labelText, width-len([]rune(num))-1)),
				clickAction, i)
		} else {
			lines = m.addPanelLine(lines,
				m.s.optionNumber.Render(num)+m.s.text.Render(labelText),
				clickAction, i)
		}
	}

	// Show inline text input when focused and editing via 't' key.
	if focused && m.editing {
		lines = m.addPanelLines(lines, m.renderInlineTextInput(width))
	} else if focused {
		lines = m.addPanelLine(lines, "", clickNone, -1)
		hint := "  t = type custom response"
		if hasTabNavigation(v) {
			hint = "  tab/s-tab=tabs  t = type"
		}
		lines = m.addPanelLine(lines, m.s.dim.Render(hint), clickNone, -1)
	}

	return lines
}

// isQuestionReason returns true if the verdict represents a question dialog.
func isQuestionReason(v *model.Verdict) bool {
	return v != nil && v.Blocked && strings.Contains(v.Reason, "question")
}

// buildQuestionPanel renders a unified question dialog: question text with ┃
// border, then interactive options (no separate dialog preview + action split).
// Matches OpenCode's layout: question text, then numbered options with
// descriptions, styled as clickable items.
func (m *tuiModel) buildQuestionPanel(v *model.Verdict, width int, lines []string) []string {
	focused := m.focus == panelActions
	borderStyle := m.s.accentBorder
	border := "┃ "
	borderWidth := 2
	contentWidth := width - borderWidth
	if contentWidth < 10 {
		contentWidth = 10
	}

	// Parse WaitingFor to separate question text from options.
	questionText, parsedItems := parseChecklist(v.WaitingFor)

	// Build a map from option label → description for enriching action buttons.
	descMap := make(map[string]string)
	for _, item := range parsedItems {
		// Clean the label: strip "N. " prefix and checkbox prefix
		label := item.label
		if idx := strings.Index(label, ". "); idx >= 0 && idx <= 2 {
			label = strings.TrimSpace(label[idx+2:])
		}
		// Strip checkbox prefix if present
		label = strings.TrimPrefix(label, "[ ] ")
		label = strings.TrimPrefix(label, "[✓] ")
		if item.description != "" {
			descMap[label] = item.description
		}
	}

	// Question text with ┃ border (skip tab header lines — already shown in tab bar)
	if questionText != "" {
		lines = m.addPanelLine(lines, "", clickNone, -1)
		for _, wl := range strings.Split(questionText, "\n") {
			wl = strings.TrimSpace(wl)
			if wl == "" || strings.HasPrefix(wl, "[tabs]") {
				continue
			}
			for _, rl := range wrapText(wl, contentWidth) {
				lines = m.addPanelLine(lines, borderStyle.Render(border)+m.s.text.Render(rl), clickNone, -1)
			}
		}
	}
	lines = m.addPanelLine(lines, "", clickNone, -1)

	// Interactive options — clean format matching OpenCode: "N. Label"
	actionIdx := 0
	for i, a := range v.Actions {
		if i >= 9 {
			break
		}
		if a.Keys == "Tab" || a.Keys == "BTab" {
			continue
		}

		// Dismiss/escape actions go at the end with different styling
		isEscape := a.Keys == "Escape" || strings.Contains(strings.ToLower(a.Label), "dismiss")
		if isEscape {
			continue
		}

		num := fmt.Sprintf(" %d.", actionIdx+1)
		labelText := " " + a.Label
		combined := truncate(num+labelText, width)
		if len(combined) > len(num) {
			labelText = combined[len(num):]
		}

		if focused && i == m.actionCursor {
			lines = m.addPanelLine(lines,
				m.s.optionActive.Render(padRight(num, len([]rune(num))+1))+
					m.s.optionBg.Render(padRight(labelText, width-len([]rune(num))-1)),
				clickAction, i)
		} else {
			lines = m.addPanelLine(lines,
				m.s.optionNumber.Render(num)+m.s.text.Render(labelText),
				clickAction, i)
		}

		// Description from parsed checklist, indented under the option
		if desc, ok := descMap[a.Label]; ok {
			descLine := truncate("   "+desc, width)
			lines = m.addPanelLine(lines, m.s.dim.Render(descLine), clickNone, -1)
		}

		actionIdx++
	}

	// Dismiss action at the bottom (dim, separate)
	for i, a := range v.Actions {
		isEscape := a.Keys == "Escape" || strings.Contains(strings.ToLower(a.Label), "dismiss")
		if !isEscape {
			continue
		}
		if a.Keys == "Tab" || a.Keys == "BTab" {
			continue
		}

		lines = m.addPanelLine(lines, "", clickNone, -1)
		label := truncate("  esc "+a.Label, width)
		if focused && i == m.actionCursor {
			lines = m.addPanelLine(lines,
				m.s.optionBg.Render(padRight(label, width)),
				clickAction, i)
		} else {
			lines = m.addPanelLine(lines, m.s.dim.Render(label), clickAction, i)
		}
	}

	// Inline text input for custom answer or 't' key
	if focused && (hasQuestionWithCustomAnswer(v) || m.editing) {
		lines = m.addPanelLines(lines, m.renderInlineTextInput(width))
	} else if focused {
		lines = m.addPanelLine(lines, "", clickNone, -1)
		hint := "  ↑↓ select  enter confirm  esc dismiss"
		if hasTabNavigation(v) {
			hint = "  ⇆ tab  ↑↓ select  enter confirm  esc dismiss"
		}
		lines = m.addPanelLine(lines, m.s.dim.Render(hint), clickNone, -1)
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
	lines = append(lines, m.s.inputLabel.Render(truncate("  Type response:", width)))

	// Render the text input widget (includes cursor when focused)
	inputView := m.textInput.View()
	lines = append(lines, "  "+inputView)

	if m.editing {
		lines = append(lines, m.s.dim.Render("  enter=submit  esc=cancel"))
	}

	return lines
}

// renderDialogContent produces agent-specific styled lines for the dialog
// that the agent is blocked on. Dispatches based on v.Agent and v.Reason to
// replicate the visual style of each agent's TUI dialogs.
func renderDialogContent(v *model.Verdict, width int, s styles) []string {
	if v.Agent == "opencode" {
		return renderOpenCodeDialog(v, width, s)
	}
	if v.Agent == "claude_code" {
		return renderClaudeCodeDialog(v, width, s)
	}
	if v.Agent == "codex" {
		return renderCodexDialog(v, width, s)
	}
	return renderGenericDialog(v, width, s)
}

// renderOpenCodeDialog renders OpenCode dialogs with the characteristic "┃"
// left border in the appropriate color (yellow=permission, blue=question, red=reject).
//
// Source: packages/opencode/src/cli/cmd/tui/component/border.tsx
// SplitBorder uses "┃" (thick vertical) as left border.
func renderOpenCodeDialog(v *model.Verdict, width int, s styles) []string {
	var lines []string
	border := "┃ "
	borderWidth := 2

	reason := strings.ToLower(v.Reason)
	var borderStyle lipgloss.Style
	switch {
	case strings.Contains(reason, "permission"):
		borderStyle = s.warningBorder
		// Title line: △ Permission required
		lines = append(lines, borderStyle.Render(border)+s.blocked.Render("△ Permission required"))
	case strings.Contains(reason, "reject"):
		borderStyle = s.errorBorder
		lines = append(lines, borderStyle.Render(border)+s.err.Render("△ Reject permission"))
	case strings.Contains(reason, "question"):
		borderStyle = s.accentBorder
		// No special title — question text comes from WaitingFor
	default:
		// Idle or other — use dim border
		borderStyle = s.dim
	}

	// Render WaitingFor content with the colored border prefix
	content := v.WaitingFor
	if content == "" {
		content = v.Reason
	}

	// Strip [tabs] prefix — already rendered as a tab bar above.
	if strings.HasPrefix(content, "[tabs] ") {
		if idx := strings.Index(content, "\n"); idx >= 0 {
			content = content[idx+1:]
		} else {
			content = ""
		}
	}

	contentWidth := width - borderWidth
	if contentWidth < 10 {
		contentWidth = 10
	}
	for _, wl := range strings.Split(content, "\n") {
		for _, rl := range wrapText(wl, contentWidth) {
			lines = append(lines, borderStyle.Render(border)+s.text.Render(rl))
		}
	}

	// Show the one-line reason as context below the dialog
	if v.WaitingFor != "" && v.Reason != "" {
		lines = append(lines, s.dim.Render(truncate(v.Reason, width)))
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
func renderClaudeCodeDialog(v *model.Verdict, width int, s styles) []string {
	var lines []string

	reason := strings.ToLower(v.Reason)
	switch {
	case strings.Contains(reason, "permission"):
		lines = append(lines, s.claudeTool.Render("Permission required"))
	case strings.Contains(reason, "edit"):
		lines = append(lines, s.claudeTool.Render("Edit approval"))
	case strings.Contains(reason, "idle"):
		lines = append(lines, s.claudeDim.Render("Idle at prompt"))
	default:
		lines = append(lines, s.claudeTool.Render("Waiting"))
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
				lines = append(lines, s.codexCommand.Render(rl))
			}
		} else {
			lines = append(lines, wrapText(wl, width)...)
		}
	}

	// Show reason as context
	if v.WaitingFor != "" && v.Reason != "" {
		lines = append(lines, s.dim.Render(truncate(v.Reason, width)))
	}

	return lines
}

// renderCodexDialog renders Codex dialogs with bold titles, "$ " commands
// in green, and "›" selection cursor.
//
// Source: codex-rs/tui/src/bottom_pane/approval_overlay.rs
// Title in bold, "$ command" prefix, "Reason: " prefix in italic,
// "›" selection cursor on options.
func renderCodexDialog(v *model.Verdict, width int, s styles) []string {
	var lines []string

	reason := strings.ToLower(v.Reason)
	switch {
	case strings.Contains(reason, "command approval"):
		lines = append(lines, s.codexTitle.Render("Command approval"))
	case strings.Contains(reason, "edit approval"):
		lines = append(lines, s.codexTitle.Render("Edit approval"))
	case strings.Contains(reason, "network"):
		lines = append(lines, s.codexTitle.Render("Network access"))
	case strings.Contains(reason, "mcp"):
		lines = append(lines, s.codexTitle.Render("MCP approval"))
	case strings.Contains(reason, "question"):
		lines = append(lines, s.codexTitle.Render("Question"))
	case strings.Contains(reason, "user input"):
		lines = append(lines, s.codexTitle.Render("User input requested"))
	case strings.Contains(reason, "idle"):
		lines = append(lines, s.dim.Render("Idle at prompt"))
	default:
		lines = append(lines, s.codexTitle.Render("Waiting"))
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
				lines = append(lines, s.codexCommand.Render(rl))
			}
		case strings.HasPrefix(trimmed, "› ") || strings.HasPrefix(trimmed, "›"):
			// Selection cursor in cyan
			for _, rl := range wrapText(wl, width) {
				lines = append(lines, s.codexCursor.Render(rl))
			}
		default:
			lines = append(lines, wrapText(wl, width)...)
		}
	}

	// Show reason as context
	if v.WaitingFor != "" && v.Reason != "" {
		lines = append(lines, s.dim.Render(truncate(v.Reason, width)))
	}

	return lines
}

// renderGenericDialog renders a generic blocked dialog for unknown agents.
// Falls back to the previous behavior: WaitingFor in yellow, reason in dim.
func renderGenericDialog(v *model.Verdict, width int, s styles) []string {
	var lines []string
	if v.WaitingFor != "" {
		for _, wl := range strings.Split(v.WaitingFor, "\n") {
			for _, rl := range wrapText(wl, width) {
				lines = append(lines, s.blocked.Render(rl))
			}
		}
	} else if v.Reason != "" {
		for _, rl := range wrapText(v.Reason, width) {
			lines = append(lines, s.blocked.Render(rl))
		}
	}
	if v.WaitingFor != "" && v.Reason != "" {
		lines = append(lines, s.dim.Render(truncate(v.Reason, width)))
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

// hasTabNavigation returns true if the verdict has Tab/BTab actions (multi-tab form).
func hasTabNavigation(v *model.Verdict) bool {
	if v == nil || !v.Blocked {
		return false
	}
	for _, a := range v.Actions {
		if a.Keys == "Tab" && a.Label == "next tab" {
			return true
		}
	}
	return false
}

// isConfirmTab returns true if the verdict represents a Confirm tab
// of a multi-question form (review + submit).
func isConfirmTab(v *model.Verdict) bool {
	return v != nil && v.Blocked && strings.Contains(v.Reason, "confirm tab")
}

// parseTabsFromWaitingFor extracts tab names from WaitingFor text.
// The parser encodes them as "[tabs] Name1 | Name2 | Confirm" on the first line.
func parseTabsFromWaitingFor(waitingFor string) []string {
	lines := strings.Split(waitingFor, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[tabs] ") {
			raw := strings.TrimPrefix(line, "[tabs] ")
			parts := strings.Split(raw, " | ")
			var tabs []string
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					tabs = append(tabs, p)
				}
			}
			return tabs
		}
	}
	return nil
}

// addPanelLine appends a line to the action panel and records its click target.
func (m *tuiModel) addPanelLine(lines []string, line string, kind panelClickKind, index int) []string {
	m.panelClicks = append(m.panelClicks, panelClickInfo{kind: kind, index: index})
	return append(lines, line)
}

// addPanelLines appends multiple non-clickable lines to the action panel.
func (m *tuiModel) addPanelLines(lines []string, newLines []string) []string {
	for _, nl := range newLines {
		lines = m.addPanelLine(lines, nl, clickNone, -1)
	}
	return lines
}

// renderTabBar renders a horizontal tab header bar for multi-tab question forms
// and records click zones so each tab is individually clickable.
// The active tab is determined by the verdict's Reason (confirm tab) or by being
// the only tab with numbered options visible.
func (m *tuiModel) renderTabBar(v *model.Verdict, width int, lines []string) []string {
	tabs := parseTabsFromWaitingFor(v.WaitingFor)
	if len(tabs) == 0 {
		return lines
	}

	// Determine which tab is active. The Confirm tab has a distinct reason.
	// Default to the first tab; override to the last tab for Confirm.
	activeTab := 0
	if isConfirmTab(v) {
		activeTab = len(tabs) - 1
	}

	var bar strings.Builder
	xPos := 0 // visible character position
	for i, tab := range tabs {
		if i > 0 {
			bar.WriteString("  ")
			xPos += 2
		}
		tabText := " " + tab + " "
		tabWidth := len([]rune(tabText))

		// Record click zone for this tab
		m.tabZones = append(m.tabZones, tabClickZone{
			xStart:   xPos,
			xEnd:     xPos + tabWidth,
			tabIndex: i,
		})

		if i == activeTab {
			bar.WriteString(m.s.activeTab.Render(tabText))
		} else {
			// Tabs before the active tab are "answered"; tabs after are "pending".
			// On the confirm tab (last), all previous tabs are answered.
			if i < activeTab || isConfirmTab(v) {
				bar.WriteString(m.s.answeredTab.Render(tabText))
			} else {
				bar.WriteString(m.s.pendingTab.Render(tabText))
			}
		}
		xPos += tabWidth
	}

	tabLine := bar.String()
	// Tab bar line is clickable (X-position checked against zones)
	lines = m.addPanelLine(lines, tabLine, clickTabBar, -1)
	// Blank separator after tab bar
	lines = m.addPanelLine(lines, "", clickNone, -1)
	return lines
}

// buildConfirmTabPanel renders the Confirm tab of a multi-question form.
// Shows tab bar, review summary content, and Submit/Dismiss actions.
func (m *tuiModel) buildConfirmTabPanel(v *model.Verdict, width int, lines []string) []string {
	focused := m.focus == panelActions
	borderStyle := m.s.accentBorder
	border := "┃ "
	borderWidth := 2
	contentWidth := width - borderWidth
	if contentWidth < 10 {
		contentWidth = 10
	}

	// Render tab header bar
	lines = m.renderTabBar(v, width, lines)

	// Extract review content from WaitingFor (after "[confirm tab]" line).
	wfLines := strings.Split(v.WaitingFor, "\n")
	inReview := false
	for _, wl := range wfLines {
		trimmed := strings.TrimSpace(wl)
		if trimmed == "[confirm tab]" {
			inReview = true
			continue
		}
		if strings.HasPrefix(trimmed, "[tabs] ") {
			continue // skip tab header line
		}
		if inReview && trimmed != "" {
			// Style review content: "(not answered)" in error red,
			// labels ending with ":" in muted, values in text color.
			for _, rl := range wrapText(trimmed, contentWidth) {
				if strings.Contains(rl, "(not answered)") {
					lines = m.addPanelLine(lines, borderStyle.Render(border)+m.s.reviewUnanswered.Render(rl), clickNone, -1)
				} else if strings.HasSuffix(strings.TrimSpace(rl), ":") {
					lines = m.addPanelLine(lines, borderStyle.Render(border)+m.s.reviewLabel.Render(rl), clickNone, -1)
				} else {
					lines = m.addPanelLine(lines, borderStyle.Render(border)+m.s.reviewValue.Render(rl), clickNone, -1)
				}
			}
		}
	}

	lines = m.addPanelLine(lines, "", clickNone, -1)

	// Actions: Submit all answers, Tab, BTab, Escape
	for i, a := range v.Actions {
		if i >= 9 {
			break
		}
		// Skip Tab/BTab from the numbered list
		if a.Keys == "Tab" || a.Keys == "BTab" {
			continue
		}

		rec := " "
		if i == v.Recommended {
			rec = "*"
		}

		riskStr := m.riskLabel(a.Risk)

		num := fmt.Sprintf(" %s%s.", rec, a.Keys)
		labelText := fmt.Sprintf(" %s (%s)", a.Label, riskStr)
		combined := truncate(num+labelText, width)
		if len(combined) > len(num) {
			labelText = combined[len(num):]
		}

		if focused && i == m.actionCursor {
			lines = m.addPanelLine(lines,
				m.s.optionActive.Render(padRight(num, len([]rune(num))+1))+
					m.s.optionBg.Render(padRight(labelText, width-len([]rune(num))-1)),
				clickAction, i)
		} else {
			lines = m.addPanelLine(lines,
				m.s.optionNumber.Render(num)+m.s.text.Render(labelText),
				clickAction, i)
		}
	}

	if focused {
		lines = m.addPanelLine(lines, "", clickNone, -1)
		lines = m.addPanelLine(lines, m.s.dim.Render("  enter=submit all  tab/s-tab=tabs  esc=dismiss"), clickNone, -1)
	}

	return lines
}

// initLocalChecks initializes the buffered selection state for a multi-select
// question verdict. It parses the initial checked state from WaitingFor and
// copies it into both localChecks (mutable) and initialChecks (immutable).
// Callers must check isMultiSelectQuestion before calling.
func (m *tuiModel) initLocalChecks(v *model.Verdict) {
	if v == nil || m.localTarget == v.Target {
		return // already initialized for this verdict
	}
	m.localTarget = v.Target
	m.localChecks = make(map[int]bool)
	m.initialChecks = make(map[int]bool)
	_, items := parseChecklist(v.WaitingFor)
	for i, item := range items {
		m.localChecks[i] = item.checked
		m.initialChecks[i] = item.checked
	}
}

// resetLocalChecks clears the buffered selection state. Called on submit,
// dismiss, or when switching to a different verdict.
func (m *tuiModel) resetLocalChecks() {
	m.localChecks = nil
	m.initialChecks = nil
	m.localTarget = ""
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
// All toggles are buffered locally — nothing is sent to the pane until Submit.
func (m *tuiModel) buildMultiSelectPanel(v *model.Verdict, width int, lines []string) []string {
	focused := m.focus == panelActions
	borderStyle := m.s.accentBorder
	border := "┃ "
	borderWidth := 2
	contentWidth := width - borderWidth
	if contentWidth < 10 {
		contentWidth = 10
	}

	// Ensure local checks are initialized for this verdict
	if isMultiSelectQuestion(v) {
		m.initLocalChecks(v)
	}

	// Render tab header bar for multi-tab question forms.
	lines = m.renderTabBar(v, width, lines)

	// Parse question text and checkbox options.
	// Use localChecks for checked state if available (buffered selection),
	// otherwise fall back to parser state from WaitingFor.
	questionText, checkItems := parseChecklist(v.WaitingFor)

	// Strip [tabs] prefix from question text (already rendered as tab bar).
	if strings.HasPrefix(questionText, "[tabs] ") {
		// Remove the [tabs] line
		tabLines := strings.SplitN(questionText, "\n", 2)
		if len(tabLines) > 1 {
			questionText = strings.TrimSpace(tabLines[1])
		} else {
			questionText = ""
		}
	}

	// Question text with purple border, text in primary text color
	if questionText != "" {
		for _, wl := range wrapText(questionText, contentWidth) {
			lines = m.addPanelLine(lines, borderStyle.Render(border)+m.s.text.Render(wl), clickNone, -1)
		}
	}
	lines = m.addPanelLine(lines, "", clickNone, -1)

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

		// Use buffered local state if available, otherwise parser state
		checked := item.checked
		if m.localChecks != nil {
			checked = m.localChecks[i]
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

		// OpenCode layout: "{number}. [{check}] {label}"
		// Number styled separately; checkbox+label as one unit.
		check := "[ ]"
		if checked {
			check = "[✓]"
		}
		num := fmt.Sprintf(" %d.", i+1)
		labelText := fmt.Sprintf(" %s %s", check, optLabel)
		combined := truncate(num+labelText, width)
		if len(combined) > len(num) {
			labelText = combined[len(num):]
		}

		if focused && m.actionCursor == i {
			// Active: tinted number + blue label, both on dark bg
			lines = m.addPanelLine(lines,
				m.s.optionActive.Render(padRight(num, len([]rune(num))+1))+
					m.s.optionBg.Render(padRight(labelText, width-len([]rune(num))-1)),
				clickToggle, i)
		} else if checked {
			// Picked: muted number, green checkbox+label
			lines = m.addPanelLine(lines,
				m.s.optionNumber.Render(num)+m.s.optionPicked.Render(labelText),
				clickToggle, i)
		} else {
			// Normal: muted number, white checkbox+label
			lines = m.addPanelLine(lines,
				m.s.optionNumber.Render(num)+m.s.text.Render(labelText),
				clickToggle, i)
		}

		// Description in muted, indented under the label
		if item.description != "" && !isCustomOpt {
			desc := "   " + item.description
			desc = truncate(desc, width)
			lines = m.addPanelLine(lines, m.s.dim.Render(desc), clickNone, -1)
		}

		// Render the generic text input under the custom answer option
		if isCustomOpt {
			lines = m.addPanelLines(lines, m.renderInlineTextInput(width))
			textInputRendered = true
		}
	}

	// Fallback: if the text input wasn't rendered under a custom option
	// (e.g., option truncated from WaitingFor), show it when editing via 't'.
	if m.editing && !textInputRendered {
		lines = m.addPanelLines(lines, m.renderInlineTextInput(width))
	}

	lines = m.addPanelLine(lines, "", clickNone, -1)

	// Submit and Dismiss actions (after toggle options in the actions array).
	// Tab/BTab are handled by the tab bar + keyboard, not shown as actions.
	toggleCount := len(checkItems)
	if toggleCount > len(v.Actions) {
		toggleCount = len(v.Actions)
	}
	for i := toggleCount; i < len(v.Actions) && i < toggleCount+4; i++ {
		a := v.Actions[i]
		if a.Keys == "Tab" || a.Keys == "BTab" {
			continue
		}
		rec := " "
		if i == v.Recommended {
			rec = "*"
		}

		num := fmt.Sprintf(" %s%s.", rec, a.Keys)
		labelText := " " + a.Label
		combined := truncate(num+labelText, width)
		if len(combined) > len(num) {
			labelText = combined[len(num):]
		}

		if focused && m.actionCursor == i {
			lines = m.addPanelLine(lines,
				m.s.optionActive.Render(padRight(num, len([]rune(num))+1))+
					m.s.optionBg.Render(padRight(labelText, width-len([]rune(num))-1)),
				clickAction, i)
		} else {
			lines = m.addPanelLine(lines,
				m.s.optionNumber.Render(num)+m.s.text.Render(labelText),
				clickAction, i)
		}
	}

	// Hint line with pending changes indicator
	if focused {
		lines = m.addPanelLine(lines, "", clickNone, -1)
		// Count pending changes
		pendingChanges := 0
		if m.localChecks != nil {
			for i, desired := range m.localChecks {
				if desired != m.initialChecks[i] {
					pendingChanges++
				}
			}
		}
		if m.textInput.Value() != "" {
			pendingChanges++
		}
		optCount := len(checkItems)
		if hasCustom {
			// Don't count the custom option in the toggle range
			optCount--
		}
		hint := "  enter=submit  esc=discard"
		if optCount > 0 {
			hint = fmt.Sprintf("  space/1-%d=toggle  enter=submit  esc=discard", optCount)
		}
		if hasTabNavigation(v) {
			hint += "  tab/s-tab=tabs"
		}
		if pendingChanges > 0 {
			hint += fmt.Sprintf("  (%d pending)", pendingChanges)
		}
		lines = m.addPanelLine(lines, m.s.dim.Render(hint), clickNone, -1)
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
	icon := m.s.dim.Render("·")
	if group != nil {
		if group.blocked > 0 {
			icon = m.s.blocked.Render("⚠")
		} else if group.active > 0 {
			icon = m.s.active.Render("✓")
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
		nameCol = m.s.selected.Render(padRight(
			fmt.Sprintf("  %s %s %s", arrow, sessionIcon(group), item.session), nameWidth))
		reasonCol = m.s.selected.Render(padRight(reason, reasonWidth))
	} else {
		nameCol = padRight(fmt.Sprintf("  %s %s %s", arrow, icon, item.session), nameWidth)
		reasonCol = m.s.dim.Render(padRight(reason, reasonWidth))
	}

	return nameCol, reasonCol
}

func (m *tuiModel) renderPaneRow(item listItem, idx, nameWidth, reasonWidth int) (string, string) {
	v := m.verdicts[item.paneIdx]

	icon := m.s.active.Render("✓")
	if v.Blocked {
		icon = m.s.blocked.Render("⚠")
	}
	if v.Agent == "error" {
		icon = m.s.err.Render("✗")
	}
	if v.Agent == "not_an_agent" {
		icon = m.s.dim.Render("·")
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
		nameCol = m.s.selected.Render(padRight(
			fmt.Sprintf("      %s %s", iconText(v), paneLabel), nameWidth))
		reasonCol = m.s.selected.Render(padRight(reason, reasonWidth))
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
func (m *tuiModel) riskLabel(risk string) string {
	switch risk {
	case "low":
		return m.s.riskLow.Render("low")
	case "medium":
		return m.s.riskMed.Render("med")
	case "high":
		return m.s.riskHigh.Render("HIGH")
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
