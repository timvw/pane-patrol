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
	status   lipgloss.Style

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
		status:   lipgloss.NewStyle().Foreground(t.TextMuted),

		hintKey:  lipgloss.NewStyle().Foreground(t.Text),
		hintDesc: lipgloss.NewStyle().Foreground(t.TextMuted),
	}
}
