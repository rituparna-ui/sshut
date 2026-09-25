// Package ui contains reusable presentation components for the TUI.
package ui

import (
	"charm.land/lipgloss/v2"
	compat "charm.land/lipgloss/v2/compat"
)

// Styles contains the shared visual language for sshut.
type Styles struct {
	Title      lipgloss.Style
	Path       lipgloss.Style
	Border     lipgloss.Style
	Active     lipgloss.Style
	Header     lipgloss.Style
	Row        lipgloss.Style
	Cursor     lipgloss.Style
	Selected   lipgloss.Style
	Directory  lipgloss.Style
	Symlink    lipgloss.Style
	Special    lipgloss.Style
	Footer     lipgloss.Style
	FooterKey  lipgloss.Style
	Status     lipgloss.Style
	Error      lipgloss.Style
	Muted      lipgloss.Style
	BorderEdge lipgloss.Style
}

// NewStyles returns terminal-adaptive colors that work on light and dark
// backgrounds.
func NewStyles() Styles {
	accent := compat.AdaptiveColor{Light: lipgloss.Color("#005f87"), Dark: lipgloss.Color("#5fd7ff")}
	green := compat.AdaptiveColor{Light: lipgloss.Color("#176b3a"), Dark: lipgloss.Color("#69d695")}
	magenta := compat.AdaptiveColor{Light: lipgloss.Color("#7a3e9d"), Dark: lipgloss.Color("#d7a1ff")}
	red := compat.AdaptiveColor{Light: lipgloss.Color("#a61b1b"), Dark: lipgloss.Color("#ff7b72")}
	muted := compat.AdaptiveColor{Light: lipgloss.Color("#666666"), Dark: lipgloss.Color("#9a9a9a")}
	border := compat.AdaptiveColor{Light: lipgloss.Color("#b8b8b8"), Dark: lipgloss.Color("#555555")}

	return Styles{
		Title:      lipgloss.NewStyle().Bold(true),
		Path:       lipgloss.NewStyle().Foreground(muted),
		Border:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border),
		Active:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent),
		Header:     lipgloss.NewStyle().Foreground(muted),
		Row:        lipgloss.NewStyle(),
		Cursor:     lipgloss.NewStyle().Foreground(accent).Bold(true),
		Selected:   lipgloss.NewStyle().Background(accent),
		Directory:  lipgloss.NewStyle().Foreground(green),
		Symlink:    lipgloss.NewStyle().Foreground(magenta),
		Special:    lipgloss.NewStyle().Foreground(muted),
		Footer:     lipgloss.NewStyle().Foreground(muted),
		FooterKey:  lipgloss.NewStyle().Foreground(accent).Bold(true),
		Status:     lipgloss.NewStyle(),
		Error:      lipgloss.NewStyle().Foreground(red),
		Muted:      lipgloss.NewStyle().Foreground(muted),
		BorderEdge: lipgloss.NewStyle().Foreground(border),
	}
}
