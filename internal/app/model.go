// Package app coordinates filesystem operations and the Bubble Tea model.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/rituu/sshut/internal/filesystem"
	"github.com/rituu/sshut/internal/local"
	remotesftp "github.com/rituu/sshut/internal/sftp"
	"github.com/rituu/sshut/internal/ui"
)

const remoteConnectTimeout = 30 * time.Second

type side uint8

const (
	localSide side = iota
	remoteSide
)

// Model is the root Bubble Tea model.
type Model struct {
	localFS     filesystem.FS
	remoteFS    filesystem.FS
	local       ui.Pane
	remote      ui.Pane
	destination string
	active      side
	styles      ui.Styles
	width       int
	height      int
	status      string
	lastErr     error
	quitting    bool
}

var _ tea.Model = Model{}

// New creates sshut for an optional SSH destination. An empty destination runs
// in local-only mode until the interactive connection prompt is added.
func New(destination string) Model {
	model := Model{
		localFS:     local.New(),
		local:       ui.NewPane("LOCAL"),
		styles:      ui.NewStyles(),
		destination: destination,
		active:      localSide,
		status:      "Ready",
	}
	if destination != "" {
		model.remote = ui.NewPane("REMOTE " + destination)
		model.remote.Loading = true
		model.status = "Connecting to " + destination + "…"
	}
	return model
}

// NewLocal creates a local-only model. It is retained as a small constructor
// for tests and the first incremental build.
func NewLocal() Model { return New("") }

// Init starts independent local and remote loading commands.
func (m Model) Init() tea.Cmd {
	localCommand := loadHome(localSide, m.localFS)
	if m.destination == "" {
		return localCommand
	}
	return tea.Batch(localCommand, connectRemote(m.destination))
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

	case remoteConnectedMsg:
		if msg.err != nil {
			m.remote.Loading = false
			m.remote.Err = msg.err
			return m.fail(msg.err)
		}
		m.remoteFS = msg.backend
		m.remote.Path = msg.path
		m.remote.Loading = false
		m.remote.Err = nil
		m.status = "Connected to " + m.destination
		return m, loadDirectory(remoteSide, m.remoteFS, msg.path)

	case directoryLoadedMsg:
		return m.applyDirectoryLoaded(msg)
	}
	return m, nil
}

func (m Model) applyDirectoryLoaded(msg directoryLoadedMsg) (tea.Model, tea.Cmd) {
	backend, pane := m.backendAndPane(msg.side)
	if backend == nil || pane == nil {
		return m, nil
	}
	pane.Loading = false
	if msg.err != nil {
		pane.Err = msg.err
		return m, nil
	}
	pane.Err = nil
	pane.Path = msg.path
	pane.SetEntries(withParent(backend, msg.path, msg.entries))
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "tab":
		m.active = m.active.other()
		return m, nil
	case "shift+tab":
		m.active = m.active.other()
		return m, nil
	}

	pane := m.activePane()
	if pane == nil {
		return m, nil
	}
	switch msg.String() {
	case "up", "k":
		pane.Move(-1)
	case "down", "j":
		pane.Move(1)
	case "pgup":
		pane.Move(-10)
	case "pgdown":
		pane.Move(10)
	case "home", "g":
		pane.Cursor = 0
	case "end", "G":
		pane.Move(max(0, len(pane.Entries)-1))
	case "space":
		pane.ToggleSelected()
		if entry, ok := pane.Current(); ok {
			pane.Move(1)
			m.status = fmt.Sprintf("Selected %s", entry.Name)
		}
	case "enter":
		return m.openCurrent()
	case "backspace", "u":
		return m.goToParent()
	case "f5":
		backend := m.activeBackend()
		if backend != nil && pane.Path != "" {
			pane.Loading = true
			pane.Err = nil
			return m, loadDirectory(m.active, backend, pane.Path)
		}
	}
	return m, nil
}

func (m Model) openCurrent() (tea.Model, tea.Cmd) {
	pane := m.activePane()
	backend := m.activeBackend()
	if pane == nil || backend == nil {
		return m, nil
	}
	entry, ok := pane.Current()
	if !ok {
		return m, nil
	}
	if entry.Name == ".." {
		return m.goToParent()
	}
	if !entry.IsDir() {
		return m, nil
	}
	pane.Loading = true
	pane.Err = nil
	return m, loadDirectory(m.active, backend, entry.Path)
}

func (m Model) goToParent() (tea.Model, tea.Cmd) {
	pane := m.activePane()
	backend := m.activeBackend()
	if pane == nil || backend == nil || pane.Path == "" {
		return m, nil
	}
	parent := backend.Parent(pane.Path)
	if parent == pane.Path {
		return m, nil
	}
	pane.Loading = true
	pane.Err = nil
	return m, loadDirectory(m.active, backend, parent)
}

func (m Model) fail(err error) (tea.Model, tea.Cmd) {
	m.lastErr = err
	return m, nil
}

func (m *Model) activePane() *ui.Pane {
	if m.active == remoteSide {
		return &m.remote
	}
	return &m.local
}

func (m *Model) activeBackend() filesystem.FS {
	if m.active == remoteSide {
		return m.remoteFS
	}
	return m.localFS
}

func (m *Model) backendAndPane(which side) (filesystem.FS, *ui.Pane) {
	if which == remoteSide {
		return m.remoteFS, &m.remote
	}
	return m.localFS, &m.local
}

func (which side) other() side {
	if which == remoteSide {
		return localSide
	}
	return remoteSide
}

// View renders one or two filesystem panes plus status and key hints.
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
	var panes string
	if m.destination == "" {
		panes = m.local.View(m.styles, max(4, width), paneHeight, true)
	} else {
		leftWidth := max(4, (width-1)/2)
		rightWidth := max(4, width-leftWidth-1)
		left := m.local.View(m.styles, leftWidth, paneHeight, m.active == localSide)
		right := m.remote.View(m.styles, rightWidth, paneHeight, m.active == remoteSide)
		divider := strings.Repeat("│\n", paneHeight-1) + "│"
		panes = lipgloss.JoinHorizontal(lipgloss.Top, left, m.styles.BorderEdge.Render(divider), right)
	}

	statusStyle := m.styles.Status
	statusText := m.status
	if m.lastErr != nil {
		statusStyle = m.styles.Error
		statusText = "Error: " + m.lastErr.Error()
	}
	keys := " tab focus  ↑/↓ move  enter open  space select  f5 refresh  q quit "
	if m.destination == "" {
		keys = " ↑/↓ move  enter open  space select  f5 refresh  q quit "
	}
	footer := lipgloss.JoinVertical(lipgloss.Left, statusStyle.Render(statusText), m.styles.Footer.Render(keys))
	body := lipgloss.JoinVertical(lipgloss.Left, panes, footer)
	view := tea.NewView(body)
	view.AltScreen = true
	view.WindowTitle = "sshut"
	return view
}

// Close releases local and remote resources after the Bubble Tea program exits.
func (m Model) Close() error {
	var errs []error
	if m.remoteFS != nil {
		if err := m.remoteFS.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close remote: %w", err))
		}
	}
	if m.localFS != nil {
		if err := m.localFS.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close local: %w", err))
		}
	}
	return errors.Join(errs...)
}

type homeLoadedMsg struct {
	side side
	path string
	err  error
}

type remoteConnectedMsg struct {
	backend filesystem.FS
	path    string
	err     error
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

func connectRemote(destination string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), remoteConnectTimeout)
		defer cancel()

		backend, err := remotesftp.Connect(ctx, destination)
		if err != nil {
			return remoteConnectedMsg{err: err}
		}
		path, err := backend.Home(ctx)
		if err != nil {
			_ = backend.Close()
			return remoteConnectedMsg{err: fmt.Errorf("resolve remote home: %w", err)}
		}
		return remoteConnectedMsg{backend: backend, path: path}
	}
}

func loadDirectory(which side, backend filesystem.FS, dir string) tea.Cmd {
	return func() tea.Msg {
		entries, err := backend.List(context.Background(), dir)
		return directoryLoadedMsg{side: which, path: dir, entries: entries, err: err}
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
