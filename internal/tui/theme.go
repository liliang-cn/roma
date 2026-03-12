package tui

import "github.com/charmbracelet/lipgloss"

type themePalette struct {
	appBg       lipgloss.Color
	accent      lipgloss.Color
	accentWarm  lipgloss.Color
	accentCool  lipgloss.Color
	success     lipgloss.Color
	warning     lipgloss.Color
	danger      lipgloss.Color
	muted       lipgloss.Color
	panelBorder lipgloss.Color
	panelBg     lipgloss.Color
	selectedBg  lipgloss.Color
	inputBorder lipgloss.Color
	text        lipgloss.Color
	textSoft    lipgloss.Color
	chipText    lipgloss.Color
}

var (
	lightPalette = themePalette{
		appBg:       lipgloss.Color("#F8FAFC"),
		accent:      lipgloss.Color("#2563EB"),
		accentWarm:  lipgloss.Color("#C2410C"),
		accentCool:  lipgloss.Color("#0284C7"),
		success:     lipgloss.Color("#16A34A"),
		warning:     lipgloss.Color("#CA8A04"),
		danger:      lipgloss.Color("#DC2626"),
		muted:       lipgloss.Color("#64748B"),
		panelBorder: lipgloss.Color("#CBD5E1"),
		panelBg:     lipgloss.Color("#FFFFFF"),
		selectedBg:  lipgloss.Color("#DBEAFE"),
		inputBorder: lipgloss.Color("#94A3B8"),
		text:        lipgloss.Color("#0F172A"),
		textSoft:    lipgloss.Color("#1E293B"),
		chipText:    lipgloss.Color("#FFFFFF"),
	}
	darkPalette = themePalette{
		appBg:       lipgloss.Color("#020617"),
		accent:      lipgloss.Color("#5E81AC"),
		accentWarm:  lipgloss.Color("#D08770"),
		accentCool:  lipgloss.Color("#88C0D0"),
		success:     lipgloss.Color("#A3BE8C"),
		warning:     lipgloss.Color("#EBCB8B"),
		danger:      lipgloss.Color("#BF616A"),
		muted:       lipgloss.Color("#7B8794"),
		panelBorder: lipgloss.Color("#3B4252"),
		panelBg:     lipgloss.Color("#111827"),
		selectedBg:  lipgloss.Color("#1F2937"),
		inputBorder: lipgloss.Color("#4C566A"),
		text:        lipgloss.Color("#E5E9F0"),
		textSoft:    lipgloss.Color("#D8DEE9"),
		chipText:    lipgloss.Color("#0B1020"),
	}
)
