// Package app coordinates filesystem operations and the Bubble Tea model.
package app

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/rituu/sshut/internal/filesystem"
	"github.com/rituu/sshut/internal/local"
	"github.com/rituu/sshut/internal/ui"
)

type side uint8

const (
	localSide side = iota
)

// Model is the root Bubble Tea model.
type Model struct {
	localFS  filesystem.FS
	local    ui.Pane
	styles   ui.Styles
	width    int
	height   int
	status   string
	lastErr  error
	quitting bool
}

var _ tea.Model = Model{}

// NewLocal creates the first runnable sshut model with a local browser.
func NewLocal() Model {
	return Model{
		localFS: local.New(),
		local:   ui.NewPane("LOCAL"),
		styles:  ui.NewStyles(),
		status:  "Ready",
	}
}

// Init starts loading the local home directory.
func (m Model) Init() tea.Cmd {
	return loadHome(localSide, m.localFS)
}

// Update handles terminal events and asynchronous filesystem results.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case homeLoadedMsg:
		if msg.side != localSide {
			return m, nil
		}
		if msg.err != nil {
			return m.fail(msg.err)
		}
		m.local.Path = msg.path
		m.local.Loading = false
		m.local.Err = nil
		m.status = "Local " + msg.path
		return m, loadDirectory(localSide, m.localFS, msg.path)

	case directoryLoadedMsg:
		if msg.side != localSide {
			return m, nil
		}
		m.local.Loading = false
		if msg.err != nil {
			m.local.Err = msg.err
			return m, nil
		}
		m.local.Err = nil
		m.local.Path = msg.path
		m.local.SetEntries(withParent(m.localFS, msg.path, msg.entries))
		return m, nil
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		m.local.Move(-1)
	case "down", "j":
		m.local.Move(1)
	case "pgup":
		m.local.Move(-10)
	case "pgdown":
		m.local.Move(10)
	case "home", "g":
		m.local.Cursor = 0
	case "end", "G":
		m.local.Move(max(0, len(m.local.Entries)-1))
	case "space":
		m.local.ToggleSelected()
		if entry, ok := m.local.Current(); ok {
			m.local.Move(1)
			m.status = fmt.Sprintf("Selected %s", entry.Name)
		}
	case "enter":
		return m.openCurrent()
	case "backspace", "u":
		return m.goToParent()
	case "f5":
		m.local.Loading = true
		return m, loadDirectory(localSide, m.localFS, m.local.Path)
	default:
		return m, nil
	}
	return m, nil
}

func (m Model) openCurrent() (tea.Model, tea.Cmd) {
	entry, ok := m.local.Current()
	if !ok {
		return m, nil
	}
	if entry.Name == ".." {
		return m.goToParent()
	}
	if !entry.IsDir() {
		return m, nil
	}
	m.local.Loading = true
	m.local.Err = nil
	return m, loadDirectory(localSide, m.localFS, entry.Path)
}

func (m Model) goToParent() (tea.Model, tea.Cmd) {
	if m.local.Path == "" {
		return m, nil
	}
	parent := m.localFS.Parent(m.local.Path)
	if parent == m.local.Path {
		return m, nil
	}
	m.local.Loading = true
	m.local.Err = nil
	return m, loadDirectory(localSide, m.localFS, parent)
}

func (m Model) fail(err error) (tea.Model, tea.Cmd) {
	m.lastErr = err
	return m, nil
}

// View renders the local browser and compact key hints.
func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	width, height := m.width, m.height
	if width <= 0 || height <= 0 {
		width, height = 80, 24
	}
	footerHeight := 2
	paneHeight := max(3, height-footerHeight)
	localWidth := max(4, width-2)
	content := m.local.View(m.styles, localWidth, paneHeight, true)
	if m.lastErr != nil {
		content = lipgloss.JoinVertical(lipgloss.Left, content, m.styles.Error.Render(m.lastErr.Error()))
	}

	footer := m.styles.Footer.Render(" ↑/↓ move  enter open  space select  f5 refresh  q quit ")
	body := lipgloss.JoinVertical(lipgloss.Left, content, footer)
	view := tea.NewView(body)
	view.AltScreen = true
	view.WindowTitle = "sshut"
	return view
}

type homeLoadedMsg struct {
	side side
	path string
	err  error
}

type directoryLoadedMsg struct {
	side    side
	path    string
	entries []filesystem.Entry
	err     error
}

func loadHome(which side, backend filesystem.FS) tea.Cmd {
	return func() tea.Msg {
		path, err := backend.Home(context.Background())
		return homeLoadedMsg{side: which, path: path, err: err}
	}
}

func loadDirectory(which side, backend filesystem.FS, path string) tea.Cmd {
	return func() tea.Msg {
		entries, err := backend.List(context.Background(), path)
		return directoryLoadedMsg{side: which, path: path, entries: entries, err: err}
	}
}

func withParent(backend filesystem.FS, current string, entries []filesystem.Entry) []filesystem.Entry {
	parent := backend.Parent(current)
	if parent == current {
		return entries
	}
	withParentEntry := make([]filesystem.Entry, 0, len(entries)+1)
	withParentEntry = append(withParentEntry, filesystem.Entry{
		Name: "..",
		Path: parent,
		Kind: filesystem.KindDirectory,
	})
	return append(withParentEntry, entries...)
}
