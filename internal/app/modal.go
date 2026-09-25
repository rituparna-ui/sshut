package app

import (
	"fmt"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"

	"github.com/rituu/sshut/internal/filesystem"
	"github.com/rituu/sshut/internal/ui"
)

type modalKind uint8

const (
	modalNone modalKind = iota
	modalNewDirectory
	modalRename
	modalGoToPath
	modalDelete
)

type modalState struct {
	kind    modalKind
	input   textinput.Model
	err     error
	target  string
	entries []filesystem.Entry
}

func newTextModal(kind modalKind, prompt, placeholder, value string) modalState {
	input := textinput.New()
	input.Prompt = prompt
	input.Placeholder = placeholder
	input.SetValue(value)
	input.SetCursor(len([]rune(value)))
	input.SetWidth(52)
	return modalState{kind: kind, input: input}
}

func (m modalState) active() bool { return m.kind != modalNone }

func (m *modalState) close() {
	m.input.Blur()
	*m = modalState{}
}

func (m modalState) view(styles ui.Styles, width, height int) string {
	title := "Input"
	var content []string
	switch m.kind {
	case modalNewDirectory:
		title = "New directory"
		content = append(content, m.input.View())
	case modalRename:
		title = "Rename"
		content = append(content, m.input.View())
	case modalGoToPath:
		title = "Go to path"
		content = append(content, m.input.View())
	case modalDelete:
		title = "Delete"
		noun := "entry"
		if len(m.entries) != 1 {
			noun = "entries"
		}
		content = append(content,
			lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("Delete %d %s?", len(m.entries), noun)),
			styles.Muted.Render(m.target),
			"",
			styles.Footer.Render("y confirm  •  n/esc cancel"),
		)
	default:
		return ""
	}
	content = append(content, styles.Footer.Render("enter confirm  •  esc cancel"))
	if m.err != nil {
		content = append(content, styles.Error.Render(m.err.Error()))
	}
	panel := styles.Active.
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		MaxWidth(max(4, width-4)).
		Render(lipgloss.JoinVertical(lipgloss.Left, append([]string{styles.Title.Render(title)}, content...)...))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
