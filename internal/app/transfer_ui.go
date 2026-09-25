package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/rituu/sshut/internal/filesystem"
	"github.com/rituu/sshut/internal/transfer"
)

type conflictAnswer struct {
	decision transfer.Decision
	applyAll bool
}

type conflictRequest struct {
	conflict transfer.Conflict
	response chan conflictAnswer
}

type conflictResolver struct {
	requests chan conflictRequest
	mu       sync.Mutex
	all      *conflictAnswer
}

func newConflictResolver() *conflictResolver {
	return &conflictResolver{requests: make(chan conflictRequest, 1)}
}

func (r *conflictResolver) reset() {
	r.mu.Lock()
	r.all = nil
	r.mu.Unlock()
}

func (r *conflictResolver) Resolve(ctx context.Context, conflict transfer.Conflict) (transfer.Decision, error) {
	r.mu.Lock()
	if r.all != nil {
		answer := *r.all
		r.mu.Unlock()
		return answer.decision, nil
	}
	r.mu.Unlock()

	request := conflictRequest{conflict: conflict, response: make(chan conflictAnswer, 1)}
	select {
	case r.requests <- request:
	case <-ctx.Done():
		return transfer.DecisionCancel, ctx.Err()
	}
	select {
	case answer := <-request.response:
		if answer.applyAll {
			r.mu.Lock()
			copy := answer
			r.all = &copy
			r.mu.Unlock()
		}
		return answer.decision, nil
	case <-ctx.Done():
		return transfer.DecisionCancel, ctx.Err()
	}
}

type transferActivityMsg struct {
	event    transfer.QueueEvent
	conflict *conflictRequest
	done     bool
}

type transferViewState struct {
	Active           bool
	Phase            transfer.Phase
	SourceLabel      string
	DestinationLabel string
	CurrentPath      string
	Bytes            int64
	TotalBytes       int64
	FilesCompleted   int
	TotalFiles       int
	Pending          int
	StartedAt        time.Time
	UpdatedAt        time.Time
	Err              error
}

func waitForTransferActivity(manager *transfer.Manager, resolver *conflictResolver) tea.Cmd {
	return func() tea.Msg {
		select {
		case event := <-manager.Events():
			return transferActivityMsg{event: event}
		case request := <-resolver.requests:
			return transferActivityMsg{conflict: &request}
		case <-manager.Done():
			return transferActivityMsg{done: true}
		}
	}
}

func (m Model) applyQueueEvent(event transfer.QueueEvent) (tea.Model, tea.Cmd) {
	status := event.Event
	transferState := &m.transferState
	if status.Phase != transfer.PhaseQueued || !transferState.Active {
		transferState.SourceLabel = event.Request.SourceLabel
		transferState.DestinationLabel = event.Request.DestinationLabel
		transferState.Phase = status.Phase
	}
	transferState.Pending = event.Pending
	transferState.UpdatedAt = time.Now()
	transferState.Err = status.Err

	switch status.Phase {
	case transfer.PhaseQueued:
		if !transferState.Active {
			transferState.Active = false
		}
	case transfer.PhasePlanning:
		transferState.Active = true
		transferState.StartedAt = time.Now()
		transferState.CurrentPath = status.CurrentPath
	case transfer.PhaseTransferring:
		transferState.Active = true
		if transferState.StartedAt.IsZero() {
			transferState.StartedAt = time.Now()
		}
		transferState.CurrentPath = status.CurrentPath
		transferState.Bytes = status.BytesTransferred
		transferState.TotalBytes = status.TotalBytes
		transferState.FilesCompleted = status.FilesCompleted
		transferState.TotalFiles = status.TotalFiles
	case transfer.PhaseComplete, transfer.PhaseSkipped, transfer.PhaseCanceled, transfer.PhaseFailed:
		transferState.Active = false
		transferState.Bytes = status.BytesTransferred
		transferState.TotalBytes = status.TotalBytes
		transferState.FilesCompleted = status.FilesCompleted
		transferState.TotalFiles = status.TotalFiles
	}

	commands := make([]tea.Cmd, 0, 2)
	if !m.activityReaderPending {
		commands = append(commands, waitForTransferActivity(m.transfers, m.conflicts))
		m.activityReaderPending = true
	}
	if terminalTransferPhase(status.Phase) {
		m.conflicts.reset()
		m.closePendingConflict()
		if target, ok := m.transferTargets[event.RequestID]; ok {
			delete(m.transferTargets, event.RequestID)
			backend, _ := m.backendAndPane(target)
			if backend != nil && event.Request.DestinationDir != "" {
				commands = append(commands, loadDirectory(target, backend, event.Request.DestinationDir))
			}
		}
	}
	if len(commands) == 0 {
		return m, nil
	}
	if len(commands) == 1 {
		return m, commands[0]
	}
	return m, tea.Batch(commands...)
}

func terminalTransferPhase(phase transfer.Phase) bool {
	switch phase {
	case transfer.PhaseComplete, transfer.PhaseSkipped, transfer.PhaseCanceled, transfer.PhaseFailed:
		return true
	default:
		return false
	}
}

func (m Model) queueTransfer(source side) (tea.Model, tea.Cmd) {
	sourceFS, sourcePane := m.backendAndPane(source)
	destination := source.other()
	destinationFS, destinationPane := m.backendAndPane(destination)
	if sourcePane == nil || sourceFS == nil || destinationPane == nil || destinationFS == nil {
		m.status = "Both local and remote panes must be connected before transferring"
		return m, nil
	}
	entries := sourcePane.Selection()
	if len(entries) == 0 {
		m.status = "Select at least one file or directory"
		return m, nil
	}
	request := transfer.Request{
		SourceFS:         sourceFS,
		DestinationFS:    destinationFS,
		DestinationDir:   destinationPane.Path,
		Entries:          entries,
		Resolver:         m.conflicts,
		SourceLabel:      sourceFS.Label(),
		DestinationLabel: destinationFS.Label(),
	}
	requestID, err := m.transfers.Enqueue(request)
	if err != nil {
		m.status = "Could not queue transfer: " + err.Error()
		return m, nil
	}
	m.transferTargets[requestID] = destination
	sourcePane.ClearSelection()
	m.status = "Queued " + transferSelectionLabel(entries)
	if !m.activityReaderPending {
		m.activityReaderPending = true
		return m, waitForTransferActivity(m.transfers, m.conflicts)
	}
	return m, nil
}

func transferSelectionLabel(entries []filesystem.Entry) string {
	if len(entries) == 1 {
		return entries[0].Name
	}
	return fmt.Sprintf("%d entries", len(entries))
}

func (m Model) transferLine(width int) string {
	state := m.transferState
	if state.Phase == "" {
		return m.styles.Muted.Render(fitTransferLine("No active transfers", width))
	}
	if state.Phase == transfer.PhaseQueued && !state.Active {
		line := fmt.Sprintf(
			"… Queued %s → %s%s",
			state.SourceLabel,
			state.DestinationLabel,
			queuedSuffix(state.Pending),
		)
		return m.styles.Muted.Render(fitTransferLine(line, width))
	}
	if state.Active {
		verb := state.Phase
		switch state.Phase {
		case transfer.PhasePlanning:
			verb = "planning"
		case transfer.PhaseTransferring:
			verb = "transferring"
		}
		line := fmt.Sprintf(
			"%s %s → %s  %s  %s",
			transferSymbol(state.Phase),
			state.SourceLabel,
			state.DestinationLabel,
			verb,
			formatBytes(state.Bytes),
		)
		if state.TotalBytes > 0 {
			line += fmt.Sprintf("/%s  %s  %s", formatBytes(state.TotalBytes), progressBar(state.Bytes, state.TotalBytes, 16), transferSpeed(state))
		}
		if state.CurrentPath != "" {
			line += "  " + filepath.Base(state.CurrentPath)
		}
		if state.Pending > 0 {
			line += fmt.Sprintf("  [%d queued]", state.Pending)
		}
		return m.styles.Status.Render(fitTransferLine(line, width))
	}

	symbol := "✓"
	style := m.styles.Directory
	message := "Transfer complete"
	switch state.Phase {
	case transfer.PhaseFailed:
		symbol, style, message = "!", m.styles.Error, "Transfer failed"
	case transfer.PhaseCanceled:
		symbol, style, message = "×", m.styles.Error, "Transfer canceled"
	case transfer.PhaseSkipped:
		symbol, style, message = "−", m.styles.Muted, "Transfer skipped"
	}
	line := fmt.Sprintf(
		"%s %s  %s/%s  %d/%d files",
		symbol,
		message,
		formatBytes(state.Bytes),
		formatBytes(state.TotalBytes),
		state.FilesCompleted,
		state.TotalFiles,
	)
	if state.Err != nil {
		line += "  " + state.Err.Error()
	}
	if state.Pending > 0 {
		line += fmt.Sprintf("  [%d queued]", state.Pending)
	}
	return style.Render(fitTransferLine(line, width))
}

func queuedSuffix(pending int) string {
	if pending <= 0 {
		return ""
	}
	return fmt.Sprintf("  [%d queued]", pending)
}

func transferSymbol(phase transfer.Phase) string {
	if phase == transfer.PhasePlanning {
		return "…"
	}
	return "→"
}

func progressBar(current, total int64, width int) string {
	if width < 4 {
		width = 4
	}
	fraction := float64(current) / float64(total)
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction * float64(width-2))
	return "[" + strings.Repeat("=", filled) + strings.Repeat(" ", width-2-filled) + "]"
}

func transferSpeed(state transferViewState) string {
	if state.StartedAt.IsZero() || state.Bytes <= 0 {
		return ""
	}
	elapsed := state.UpdatedAt.Sub(state.StartedAt).Seconds()
	if elapsed <= 0 {
		return ""
	}
	bytesPerSecond := float64(state.Bytes) / elapsed
	if bytesPerSecond <= 0 {
		return ""
	}
	remaining := state.TotalBytes - state.Bytes
	if remaining < 0 {
		remaining = 0
	}
	eta := time.Duration(float64(remaining) / bytesPerSecond * float64(time.Second))
	return fmt.Sprintf("%s/s ETA %s", formatBytes(int64(bytesPerSecond)), eta.Round(time.Second))
}

func formatBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor, exponent := int64(unit), 0
	for size := value / unit; size >= unit && exponent < 4; size /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(divisor), "KMGTP"[exponent])
}

func fitTransferLine(value string, width int) string {
	if width < 1 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(value)
}

func (m *Model) openConflict(request *conflictRequest) tea.Cmd {
	m.pendingConflict = request
	m.modal = modalState{
		kind:   modalConflict,
		target: request.conflict.Source.Name,
		message: fmt.Sprintf(
			"%s (%s) → %s (%s)",
			request.conflict.Source.Name,
			request.conflict.Source.Kind,
			request.conflict.Destination.Name,
			request.conflict.Destination.Kind,
		),
	}
	m.modal.input.Blur()
	return nil
}

func (m *Model) closePendingConflict() {
	if m.pendingConflict != nil {
		m.pendingConflict = nil
	}
	if m.modal.kind == modalConflict {
		m.modal.close()
	}
}

func (m Model) handleConflictKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.pendingConflict == nil {
		m.modal.close()
		return m, nil
	}
	key := msg.String()
	var answer conflictAnswer
	switch key {
	case "ctrl+c":
		answer = conflictAnswer{decision: transfer.DecisionCancel}
		m.pendingConflict.response <- answer
		m.closePendingConflict()
		m.quitting = true
		return m, tea.Quit
	case "x", "X", "esc":
		answer = conflictAnswer{decision: transfer.DecisionCancel}
	case "o":
		answer = conflictAnswer{decision: transfer.DecisionOverwrite}
	case "O", "shift+o":
		answer = conflictAnswer{decision: transfer.DecisionOverwrite, applyAll: true}
	case "s":
		answer = conflictAnswer{decision: transfer.DecisionSkip}
	case "S", "shift+s":
		answer = conflictAnswer{decision: transfer.DecisionSkip, applyAll: true}
	case "k":
		answer = conflictAnswer{decision: transfer.DecisionKeepBoth}
	case "K", "shift+k":
		answer = conflictAnswer{decision: transfer.DecisionKeepBoth, applyAll: true}
	default:
		return m, nil
	}
	m.pendingConflict.response <- answer
	m.status = "Conflict: " + answer.decision.String()
	if answer.applyAll {
		m.status += " (apply to all)"
	}
	m.closePendingConflict()
	return m, nil
}
