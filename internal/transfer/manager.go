package transfer

import (
	"context"
	"errors"
	"sync"

	"github.com/rituu/sshut/internal/filesystem"
)

var (
	ErrManagerClosed  = errors.New("transfer manager is closed")
	ErrInvalidRequest = errors.New("invalid transfer request")
)

// Request is one selected set of entries copied into a destination directory.
type Request struct {
	SourceFS       filesystem.FS
	DestinationFS  filesystem.FS
	DestinationDir string
	Entries        []filesystem.Entry
	Resolver       Resolver
}

// QueueEvent updates the state of a queued or active request.
type QueueEvent struct {
	RequestID uint64
	Request   Request
	Pending   int
	Event     Event
}

// Manager owns one sequential transfer worker.
type Manager struct {
	ctx    context.Context
	cancel context.CancelFunc
	events chan QueueEvent
	done   chan struct{}

	mu           sync.Mutex
	queue        []queuedRequest
	nextID       uint64
	running      bool
	closed       bool
	activeID     uint64
	activeCancel context.CancelFunc
	workerDone   sync.WaitGroup
	closeOnce    sync.Once
}

type queuedRequest struct {
	id      uint64
	request Request
}

// NewManager starts a transfer queue that stops when ctx or Close is canceled.
func NewManager(ctx context.Context) *Manager {
	managerContext, cancel := context.WithCancel(ctx)
	return &Manager{
		ctx:    managerContext,
		cancel: cancel,
		events: make(chan QueueEvent, 128),
		done:   make(chan struct{}),
	}
}

// Enqueue adds a request and returns its stable identifier.
func (m *Manager) Enqueue(request Request) (uint64, error) {
	if request.SourceFS == nil || request.DestinationFS == nil ||
		request.DestinationDir == "" || len(request.Entries) == 0 {
		return 0, ErrInvalidRequest
	}
	request.Entries = append([]filesystem.Entry(nil), request.Entries...)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0, ErrManagerClosed
	}
	m.nextID++
	queued := queuedRequest{id: m.nextID, request: request}
	m.queue = append(m.queue, queued)
	pending := len(m.queue)
	startWorker := !m.running
	if startWorker {
		m.running = true
		m.workerDone.Add(1)
	}
	m.mu.Unlock()

	m.send(QueueEvent{
		RequestID: queued.id,
		Request:   request,
		Pending:   pending,
		Event: Event{
			Phase:       PhaseQueued,
			CurrentPath: request.DestinationDir,
		},
	})
	if startWorker {
		go m.runWorker()
	}
	return queued.id, nil
}

// Events returns the queue's update stream.
func (m *Manager) Events() <-chan QueueEvent { return m.events }

// Done closes after the worker and all active I/O have stopped.
func (m *Manager) Done() <-chan struct{} { return m.done }

// CancelActive cancels only the current request. Pending requests remain queued.
func (m *Manager) CancelActive() {
	m.mu.Lock()
	cancel := m.activeCancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Pending returns the number of requests waiting behind the active request.
func (m *Manager) Pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue)
}

// Close cancels active work, discards pending requests, and waits for cleanup.
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.queue = nil
		activeCancel := m.activeCancel
		m.mu.Unlock()

		if activeCancel != nil {
			activeCancel()
		}
		m.cancel()
		m.workerDone.Wait()
		close(m.done)
	})
}

func (m *Manager) runWorker() {
	defer m.workerDone.Done()
	for {
		m.mu.Lock()
		if m.closed || len(m.queue) == 0 {
			m.running = false
			m.mu.Unlock()
			return
		}
		queued := m.queue[0]
		m.queue = m.queue[1:]
		pending := len(m.queue)
		jobContext, cancel := context.WithCancel(m.ctx)
		m.activeID = queued.id
		m.activeCancel = cancel
		m.mu.Unlock()

		m.process(jobContext, queued.id, queued.request, pending)
		cancel()

		m.mu.Lock()
		if m.activeID == queued.id {
			m.activeID = 0
			m.activeCancel = nil
		}
		m.mu.Unlock()
	}
}

func (m *Manager) process(ctx context.Context, requestID uint64, request Request, pending int) {
	base := QueueEvent{RequestID: requestID, Request: request, Pending: pending}
	m.sendEvent(base, Event{Phase: PhasePlanning, CurrentPath: request.DestinationDir})

	plan, err := BuildPlan(
		ctx,
		request.SourceFS,
		request.DestinationFS,
		request.DestinationDir,
		request.Entries,
		request.Resolver,
	)
	if err != nil {
		phase := PhaseFailed
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			phase = PhaseCanceled
		}
		m.sendEvent(base, Event{Phase: phase, Err: err})
		return
	}
	_ = Execute(ctx, plan, request.SourceFS, request.DestinationFS, func(event Event) {
		m.sendEvent(base, event)
	})
}

func (m *Manager) sendEvent(base QueueEvent, event Event) {
	base.Event = event
	m.send(base)
}

func (m *Manager) send(event QueueEvent) {
	if event.Event.Phase != PhaseQueued {
		m.mu.Lock()
		event.Pending = len(m.queue)
		m.mu.Unlock()
	}
	select {
	case m.events <- event:
	case <-m.ctx.Done():
	}
}
