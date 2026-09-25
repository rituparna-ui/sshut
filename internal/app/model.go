// Package app coordinates filesystem operations and the Bubble Tea model.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/rituu/sshut/internal/filesystem"
	"github.com/rituu/sshut/internal/local"
	remotesftp "github.com/rituu/sshut/internal/sftp"
	"github.com/rituu/sshut/internal/transfer"
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
	localFS               filesystem.FS
	remoteFS              filesystem.FS
	local                 ui.Pane
	remote                ui.Pane
	destination           string
	active                side
	styles                ui.Styles
	width                 int
	height                int
	status                string
	lastErr               error
	quitting              bool
	prompting             bool
	promptErr             error
	connectInput          textinput.Model
	modal                 modalState
	transfers             *transfer.Manager
	conflicts             *conflictResolver
	activityReaderPending bool
	transferState         transferViewState
	transferTargets       map[uint64]side
	pendingConflict       *conflictRequest
}

var _ tea.Model = Model{}

// New creates sshut for an optional SSH destination. An empty destination
// opens the interactive connection prompt.
func New(destination string) Model {
	resolver := newConflictResolver()
	model := Model{
		localFS:         local.New(),
		local:           ui.NewPane("LOCAL"),
		styles:          ui.NewStyles(),
		active:          localSide,
		status:          "Ready",
		transfers:       transfer.NewManager(context.Background()),
		conflicts:       resolver,
		transferTargets: make(map[uint64]side),
	}
	if destination == "" {
		model.showConnectionPrompt()
	} else {
		model.startRemote(destination)
	}
	return model
}

// NewLocal creates a local-only model without the startup prompt.
func NewLocal() Model {
	model := New("")
	model.prompting = false
	model.connectInput = textinput.Model{}
	model.status = "Local only"
	return model
}

func (m *Model) showConnectionPrompt() {
	input := textinput.New()
	input.Prompt = "SSH destination: "
	input.Placeholder = "user@host or ~/.ssh/config alias"
	input.CharLimit = 256
	input.SetWidth(48)
	m.connectInput = input
	m.prompting = true
	m.promptErr = nil
	m.status = "Connect a remote host"
}

func (m *Model) startRemote(destination string) {
	m.destination = destination
	m.remote = ui.NewPane("REMOTE " + destination)
	m.remote.Loading = true
	m.prompting = false
	m.promptErr = nil
	m.lastErr = nil
	m.connectInput.Blur()
	m.status = "Connecting to " + destination + "…"
}

// Init starts independent local and remote loading commands.
func (m Model) Init() tea.Cmd {
	localCommand := loadHome(localSide, m.localFS)
	if m.prompting {
		return tea.Batch(localCommand, m.connectInput.Focus())
	}
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

	case mutationDoneMsg:
		return m.applyMutationResult(msg)

	case pathResolvedMsg:
		backend, pane := m.backendAndPane(msg.side)
		if backend == nil || pane == nil {
			return m, nil
		}
		if msg.err != nil {
			pane.Loading = false
			return m.fail(msg.err)
		}
		pane.Loading = true
		pane.Err = nil
		m.lastErr = nil
		return m, loadDirectory(msg.side, backend, msg.path)

	case transferActivityMsg:
		m.activityReaderPending = false
		if msg.done {
			return m, nil
		}
		if msg.conflict != nil {
			command := m.openConflict(msg.conflict)
			next := waitForTransferActivity(m.transfers, m.conflicts)
			m.activityReaderPending = true
			if command != nil {
				return m, tea.Batch(command, next)
			}
			return m, next
		}
		return m.applyQueueEvent(msg.event)
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
	if m.modal.active() {
		if m.modal.kind == modalConflict {
			return m.handleConflictKey(msg)
		}
		return m.handleModalKey(msg)
	}
	if m.prompting {
		return m.handlePromptKey(msg)
	}

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
	case "right":
		return m.queueTransfer(localSide)
	case "left":
		return m.queueTransfer(remoteSide)
	case "ctrl+x":
		m.transfers.CancelActive()
		m.status = "Cancelling active transfer…"
		return m, nil
	case "c":
		if m.destination != "" && m.remoteFS == nil {
			m.showConnectionPrompt()
		}
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
	case "home":
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
	case "n":
		return m.openNewDirectory()
	case "r":
		return m.openRename()
	case "d":
		return m.openDeleteConfirmation()
	case "g":
		return m.openGoToPath()
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

func (m Model) handlePromptKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		m.quitting = true
		return m, tea.Quit
	case "enter":
		destination := strings.TrimSpace(m.connectInput.Value())
		if destination == "" {
			m.promptErr = fmt.Errorf("SSH destination cannot be empty")
			return m, nil
		}
		m.startRemote(destination)
		return m, connectRemote(destination)
	}

	var command tea.Cmd
	m.connectInput, command = m.connectInput.Update(msg)
	m.promptErr = m.connectInput.Err
	return m, command
}

func (m Model) applyMutationResult(msg mutationDoneMsg) (tea.Model, tea.Cmd) {
	backend, pane := m.backendAndPane(msg.side)
	if backend == nil || pane == nil {
		return m, nil
	}
	pane.Loading = false
	if msg.err != nil {
		m.lastErr = msg.err
		m.status = fmt.Sprintf("%s failed", msg.kind)
	} else {
		pane.ClearSelection()
		m.lastErr = nil
		m.status = fmt.Sprintf("%s complete", msg.kind)
	}
	pane.Err = nil
	return m, loadDirectory(msg.side, backend, msg.refreshPath)
}

func (m Model) openNewDirectory() (tea.Model, tea.Cmd) {
	pane, backend := m.activePane(), m.activeBackend()
	if pane == nil || backend == nil || pane.Path == "" {
		return m, nil
	}
	m.modal = newTextModal(modalNewDirectory, "Name: ", "new-directory", "")
	return m, m.modal.input.Focus()
}

func (m Model) openRename() (tea.Model, tea.Cmd) {
	pane, backend := m.activePane(), m.activeBackend()
	if pane == nil || backend == nil {
		return m, nil
	}
	entries := pane.Selection()
	if len(entries) != 1 {
		if len(entries) > 1 {
			m.status = "Rename accepts one selected entry"
		}
		return m, nil
	}
	entry := entries[0]
	if entry.Name == ".." {
		return m, nil
	}
	m.modal = newTextModal(modalRename, "Rename to: ", entry.Name, entry.Name)
	return m, m.modal.input.Focus()
}

func (m Model) openDeleteConfirmation() (tea.Model, tea.Cmd) {
	pane := m.activePane()
	if pane == nil {
		return m, nil
	}
	entries := pane.Selection()
	if len(entries) == 0 {
		return m, nil
	}
	m.modal = modalState{
		kind:    modalDelete,
		target:  joinSelectedSummary(entries),
		entries: entries,
	}
	return m, nil
}

func (m Model) openGoToPath() (tea.Model, tea.Cmd) {
	pane := m.activePane()
	if pane == nil || pane.Path == "" {
		return m, nil
	}
	m.modal = newTextModal(modalGoToPath, "Path: ", pane.Path, pane.Path)
	return m, m.modal.input.Focus()
}

func (m Model) handleModalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.modal.close()
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.modal.close()
		return m, nil
	}

	if m.modal.kind == modalDelete {
		switch msg.String() {
		case "y", "Y":
			pane := m.activePane()
			backend := m.activeBackend()
			directory := pane.Path
			entries := append([]filesystem.Entry(nil), m.modal.entries...)
			m.modal.close()
			if backend == nil {
				return m, nil
			}
			pane.Loading = true
			return m, deleteEntries(m.active, backend, directory, entries)
		case "n", "N":
			m.modal.close()
			return m, nil
		default:
			return m, nil
		}
	}

	if msg.String() == "enter" {
		value := strings.TrimSpace(m.modal.input.Value())
		pane, backend := m.activePane(), m.activeBackend()
		if backend == nil || pane == nil {
			m.modal.close()
			return m, nil
		}
		switch m.modal.kind {
		case modalNewDirectory:
			if err := filesystem.ValidateName(value); err != nil {
				m.modal.err = err
				return m, nil
			}
			directory := pane.Path
			m.modal.close()
			pane.Loading = true
			return m, createDirectory(m.active, backend, directory, value)
		case modalRename:
			if err := filesystem.ValidateName(value); err != nil {
				m.modal.err = err
				return m, nil
			}
			entries := pane.Selection()
			if len(entries) != 1 || entries[0].Name == ".." {
				m.modal.close()
				return m, nil
			}
			entry := entries[0]
			newPath := backend.Join(pane.Path, value)
			if newPath == entry.Path {
				m.modal.close()
				return m, nil
			}
			m.modal.close()
			pane.Loading = true
			return m, renameEntry(m.active, backend, entry.Path, newPath)
		case modalGoToPath:
			if value == "" {
				m.modal.err = fmt.Errorf("path cannot be empty")
				return m, nil
			}
			directory := pane.Path
			if !strings.HasPrefix(value, "/") && !strings.Contains(value, `:\`) {
				value = backend.Join(directory, value)
			}
			m.modal.close()
			pane.Loading = true
			return m, resolveDirectory(m.active, backend, value)
		}
	}

	var command tea.Cmd
	m.modal.input, command = m.modal.input.Update(msg)
	m.modal.err = m.modal.input.Err
	return m, command
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

func (m Model) connectionView(width, height int) tea.View {
	input := m.connectInput
	input.SetWidth(max(8, width-32))
	content := []string{
		m.styles.Title.Render("sshut"),
		m.styles.Muted.Render("Local and remote files over system OpenSSH/SFTP"),
		"",
		input.View(),
		m.styles.Footer.Render("enter connect  •  esc quit"),
	}
	if m.promptErr != nil {
		content = append(content, m.styles.Error.Render(m.promptErr.Error()))
	}
	panel := m.styles.Active.
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		MaxWidth(max(4, width-2)).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
	view := tea.NewView(lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel))
	view.AltScreen = true
	view.WindowTitle = "sshut — connect"
	return view
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
	if m.prompting {
		return m.connectionView(width, height)
	}
	if m.modal.active() {
		view := tea.NewView(m.modal.view(m.styles, width, height))
		view.AltScreen = true
		view.WindowTitle = "sshut"
		return view
	}

	footerHeight := 3
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
	keys := " ← download  → upload  tab focus  enter open  space select  n/r/d/g  f5  q quit "
	transferLine := m.transferLine(width)
	footer := lipgloss.JoinVertical(
		lipgloss.Left,
		transferLine,
		statusStyle.Render(statusText),
		m.styles.Footer.Render(keys),
	)
	body := lipgloss.JoinVertical(lipgloss.Left, panes, footer)
	view := tea.NewView(body)
	view.AltScreen = true
	view.WindowTitle = "sshut"
	return view
}

// Close releases local and remote resources after the Bubble Tea program exits.
func (m Model) Close() error {
	var errs []error
	if m.transfers != nil {
		m.transfers.Close()
	}
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
