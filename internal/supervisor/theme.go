package supervisor

import "github.com/charmbracelet/lipgloss"

// Theme defines all colors used by the supervisor TUI.
// Use DarkTheme() or LightTheme() to get a pre-built theme,
// or construct a custom Theme.
type Theme struct {
	Primary         lipgloss.Color // warm accent — cursor, title
	Secondary       lipgloss.Color // cool accent — active/selected option text
	Accent          lipgloss.Color // dialog borders, active tab bg
	Error           lipgloss.Color // errors, unanswered, reject
	Warning         lipgloss.Color // permission dialogs, blocked
	Success         lipgloss.Color // checkmarks, active status, low risk
	Info            lipgloss.Color // informational — tool names
	Text            lipgloss.Color // primary text
	TextMuted       lipgloss.Color // secondary text — descriptions, hints
	BackgroundPanel lipgloss.Color // dialog panel background
	BackgroundElem  lipgloss.Color // highlighted option background
	Border          lipgloss.Color // separators, borders
	// Computed: tint(TextMuted, Secondary, 0.6) for active option numbers.
	ActiveNumber lipgloss.Color
}

// DarkTheme returns the default dark theme matching OpenCode's color scheme.
// Source: packages/opencode/src/cli/cmd/tui/context/theme/opencode.json
func DarkTheme() Theme {
	return Theme{
		Primary:         lipgloss.Color("#fab283"),
		Secondary:       lipgloss.Color("#5c9cf5"),
		Accent:          lipgloss.Color("#9d7cd8"),
		Error:           lipgloss.Color("#e06c75"),
		Warning:         lipgloss.Color("#f5a742"),
		Success:         lipgloss.Color("#7fd88f"),
		Info:            lipgloss.Color("#56b6c2"),
		Text:            lipgloss.Color("#eeeeee"),
		TextMuted:       lipgloss.Color("#808080"),
		BackgroundPanel: lipgloss.Color("#141414"),
		BackgroundElem:  lipgloss.Color("#1e1e1e"),
		Border:          lipgloss.Color("#484848"),
		ActiveNumber:    lipgloss.Color("#6a91c6"), // tint(#808080, #5c9cf5, 0.6)
	}
}

// LightTheme returns a light theme for bright terminal backgrounds.
func LightTheme() Theme {
	return Theme{
		Primary:         lipgloss.Color("#b35c00"),
		Secondary:       lipgloss.Color("#0550ae"),
		Accent:          lipgloss.Color("#6639ba"),
		Error:           lipgloss.Color("#cf222e"),
		Warning:         lipgloss.Color("#bf8700"),
		Success:         lipgloss.Color("#116329"),
		Info:            lipgloss.Color("#0969da"),
		Text:            lipgloss.Color("#1f2328"),
		TextMuted:       lipgloss.Color("#656d76"),
		BackgroundPanel: lipgloss.Color("#ffffff"),
		BackgroundElem:  lipgloss.Color("#f6f8fa"),
		Border:          lipgloss.Color("#d0d7de"),
		ActiveNumber:    lipgloss.Color("#3660a0"), // tint(#656d76, #0550ae, 0.6)
	}
}

// ThemeByName returns a theme by name. Defaults to dark.
func ThemeByName(name string) Theme {
	switch name {
	case "light":
		return LightTheme()
	default:
		return DarkTheme()
	}
}

// styles holds all lipgloss styles derived from a Theme.
// Constructed once from a Theme and stored in tuiModel.
type styles struct {
	title    lipgloss.Style
	header   lipgloss.Style
	selected lipgloss.Style
	blocked  lipgloss.Style
	active   lipgloss.Style
	err      lipgloss.Style
	dim      lipgloss.Style
	text     lipgloss.Style
	riskLow  lipgloss.Style
	riskMed  lipgloss.Style
	riskHigh lipgloss.Style
	status   lipgloss.Style

	// Dialog borders
	warningBorder lipgloss.Style
	accentBorder  lipgloss.Style
	errorBorder   lipgloss.Style

	// Tab bar
	activeTab   lipgloss.Style
	answeredTab lipgloss.Style
	pendingTab  lipgloss.Style

	// Confirm tab review
	reviewLabel      lipgloss.Style
	reviewValue      lipgloss.Style
	reviewUnanswered lipgloss.Style

	// Options
	optionPicked lipgloss.Style
	optionNumber lipgloss.Style
	optionActive lipgloss.Style // active number (tinted toward secondary)
	checkmark    lipgloss.Style
	optionBg     lipgloss.Style // active option: secondary on backgroundElem

	// Text input
	inputLabel lipgloss.Style

	// Agent-specific
	claudeTool   lipgloss.Style
	claudeDim    lipgloss.Style
	codexTitle   lipgloss.Style
	codexCommand lipgloss.Style
	codexCursor  lipgloss.Style

	// Hints
	hintKey  lipgloss.Style
	hintDesc lipgloss.Style
}

// newStyles builds all styles from a theme.
func newStyles(t Theme) styles {
	return styles{
		title:    lipgloss.NewStyle().Bold(true).Foreground(t.Primary),
		header:   lipgloss.NewStyle().Foreground(t.Border),
		selected: lipgloss.NewStyle().Bold(true).Foreground(t.Secondary).Background(t.BackgroundElem),
		blocked:  lipgloss.NewStyle().Foreground(t.Warning),
		active:   lipgloss.NewStyle().Foreground(t.Success),
		err:      lipgloss.NewStyle().Foreground(t.Error),
		dim:      lipgloss.NewStyle().Foreground(t.TextMuted),
		text:     lipgloss.NewStyle().Foreground(t.Text),
		riskLow:  lipgloss.NewStyle().Foreground(t.Success),
		riskMed:  lipgloss.NewStyle().Foreground(t.Warning),
		riskHigh: lipgloss.NewStyle().Foreground(t.Error).Bold(true),
		status:   lipgloss.NewStyle().Foreground(t.TextMuted),

		warningBorder: lipgloss.NewStyle().Foreground(t.Warning),
		accentBorder:  lipgloss.NewStyle().Foreground(t.Accent),
		errorBorder:   lipgloss.NewStyle().Foreground(t.Error),

		activeTab:   lipgloss.NewStyle().Bold(true).Foreground(t.Text).Background(t.Accent),
		answeredTab: lipgloss.NewStyle().Foreground(t.Text),
		pendingTab:  lipgloss.NewStyle().Foreground(t.TextMuted),

		reviewLabel:      lipgloss.NewStyle().Foreground(t.TextMuted),
		reviewValue:      lipgloss.NewStyle().Foreground(t.Text),
		reviewUnanswered: lipgloss.NewStyle().Foreground(t.Error),

		optionPicked: lipgloss.NewStyle().Foreground(t.Success),
		optionNumber: lipgloss.NewStyle().Foreground(t.TextMuted),
		optionActive: lipgloss.NewStyle().Foreground(t.ActiveNumber).Background(t.BackgroundElem),
		checkmark:    lipgloss.NewStyle().Foreground(t.Success),
		optionBg:     lipgloss.NewStyle().Foreground(t.Secondary).Background(t.BackgroundElem),

		inputLabel: lipgloss.NewStyle().Foreground(t.TextMuted),

		claudeTool:   lipgloss.NewStyle().Foreground(t.Info),
		claudeDim:    lipgloss.NewStyle().Foreground(t.TextMuted),
		codexTitle:   lipgloss.NewStyle().Bold(true).Foreground(t.Text),
		codexCommand: lipgloss.NewStyle().Foreground(t.Success),
		codexCursor:  lipgloss.NewStyle().Foreground(t.Primary),

		hintKey:  lipgloss.NewStyle().Foreground(t.Text),
		hintDesc: lipgloss.NewStyle().Foreground(t.TextMuted),
	}
}
