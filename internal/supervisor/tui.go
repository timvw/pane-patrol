package supervisor

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/timvw/pane-patrol/internal/model"
)

// Styles are stored in tuiModel.s (built from the configurable Theme).
// See theme.go for Theme struct and DarkTheme()/LightTheme() constructors.

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

	// display filter
	filter displayFilter

	// grouped list
	groups          []sessionGroup
	expanded        map[string]bool // session name -> expanded
	manualCollapsed map[string]bool // sessions the user explicitly collapsed (immune to auto-expand)
	items           []listItem      // visible items (rebuilt on verdicts/expand change)

	// layout (computed in viewVerdictList, used for mouse hit testing)
	listStart int // scroll offset for list (for mouse hit testing)

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

// selectedItemKey returns a stable identifier for the currently selected item.
// For pane items: the pane's Target (e.g. "session:window.pane").
// For session headers: the session name prefixed with "session:" to avoid
// collisions with pane targets.
// Returns "" if nothing is selected.
func (m *tuiModel) selectedItemKey() string {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return ""
	}
	item := m.items[m.cursor]
	if item.kind == itemPane {
		return m.verdicts[item.paneIdx].Target
	}
	return "session:" + item.session
}

// restoreCursorByKey attempts to find the item matching the given key (from
// selectedItemKey) in the current items list and moves the cursor there.
// Falls back to clamping + skipping session headers if the key is not found.
func (m *tuiModel) restoreCursorByKey(key string) {
	if key == "" {
		m.clampCursorToPane()
		return
	}
	for i, item := range m.items {
		if item.kind == itemPane && m.verdicts[item.paneIdx].Target == key {
			m.cursor = i
			return
		}
		if item.kind == itemSession && "session:"+item.session == key {
			m.cursor = i
			return
		}
	}
	// Key not found (pane disappeared) — clamp and skip headers.
	m.clampCursorToPane()
}

// clampCursorToPane clamps cursor to valid range and advances past session
// headers so the cursor lands on a pane.
func (m *tuiModel) clampCursorToPane() {
	if m.cursor >= len(m.items) {
		m.cursor = 0
	}
	for m.cursor < len(m.items)-1 && m.items[m.cursor].kind == itemSession {
		m.cursor++
	}
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
			// Preserve cursor position across rebuild: save the selected
			// item's stable key before replacing verdicts/items.
			prevKey := m.selectedItemKey()

			m.verdicts = msg.result.Verdicts
			m.scanCount++
			m.totalCacheHits += msg.result.CacheHits

			m.rebuildGroups()
			m.restoreCursorByKey(prevKey)
		}
		// Schedule next auto-refresh and auto-nudge (both async).
		var cmds []tea.Cmd
		if cmd := m.scheduleTick(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.autoNudgeCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case nudgeResultMsg:
		if len(msg.messages) > 0 {
			m.message = strings.Join(msg.messages, " | ")
		}
		return m, nil

	case tickMsg:
		if m.scanning {
			return m, m.scheduleTick()
		}
		m.scanning = true
		return m, m.doScan()
	}

	return m, nil
}

func (m *tuiModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.handleVerdictListKey(msg)
}

func (m *tuiModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Hover: move cursor to hovered item.
	if msg.Action == tea.MouseActionMotion {
		idx := msg.Y - 1 + m.listStart
		if idx >= 0 && idx < len(m.items) {
			m.cursor = idx
		}
		return m, nil
	}

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	// Click in the list panel: header line is row 0, items start at row 1
	clickedIdx := msg.Y - 1 + m.listStart // offset for header line + scroll
	if clickedIdx < 0 || clickedIdx >= len(m.items) {
		return m, nil
	}

	m.cursor = clickedIdx
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
		}

	case "down", "j":
		if len(m.items) > 0 && m.cursor < len(m.items)-1 {
			m.cursor++
			// Skip session headers — only panes are actionable
			for m.cursor < len(m.items)-1 && m.items[m.cursor].kind == itemSession {
				m.cursor++
			}
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
		m.clampCursorToPane()
		return m, nil

	case "r":
		// Rescan
		m.scanning = true
		m.message = ""
		return m, m.doScan()
	}

	return m, nil
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
	autoLabel := "a=auto:OFF"
	if m.autoNudge {
		autoLabel = fmt.Sprintf("a=auto:ON(%s)", m.autoNudgeMaxRisk)
	}
	filterLabel := fmt.Sprintf("f=%s", m.filter)
	b.WriteString(m.styleHeaderHints(fmt.Sprintf("↑↓=nav  enter=jump  %s  %s  r=rescan  q=quit", filterLabel, autoLabel)))
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

	// Layout: 2-column list (name | reason)
	nameWidth := 10
	for _, g := range m.groups {
		if len(g.name)+6 > nameWidth {
			nameWidth = len(g.name) + 6
		}
	}
	nameWidth += 6 // icon + indent + cursor + padding

	separator := " | "
	sepWidth := len(separator)

	// Reason gets all remaining width
	reasonWidth := m.width - nameWidth - sepWidth
	if reasonWidth < 15 {
		reasonWidth = 15
	}

	// Height budget: header(1) + list + summary(1) + hints(1) + status(0-1)
	overhead := 3 // header + summary + hints
	if m.message != "" {
		overhead++
	}
	available := m.height - overhead
	if available < 6 {
		available = 6
	}

	listHeight := len(m.items)
	if listHeight > available {
		listHeight = available
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

	// Store scroll offset for mouse hit testing
	m.listStart = start

	// Render list rows (2 columns: name | reason)
	sep := m.s.header.Render(separator)
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

	// Navigation hints
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
	return m.styleHints("  ↑↓ navigate  enter jump  →/l expand  ←/h collapse  r rescan  f filter  a auto-nudge  q quit")
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

func (m *tuiModel) renderSessionRow(item listItem, idx, nameWidth, reasonWidth int) (string, string) {
	// Find the session group for aggregate info
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
