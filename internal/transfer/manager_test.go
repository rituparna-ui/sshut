package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rituu/sshut/internal/filesystem"
	"github.com/rituu/sshut/internal/local"
)

func TestManagerRunsRequestsSequentially(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	source := local.NewAt(sourceRoot)
	destination := local.NewAt(destinationRoot)
	writeTestFile(t, filepath.Join(sourceRoot, "first.txt"), "first")
	writeTestFile(t, filepath.Join(sourceRoot, "second.txt"), "second")
	first := statTestEntry(t, source, filepath.Join(sourceRoot, "first.txt"))
	second := statTestEntry(t, source, filepath.Join(sourceRoot, "second.txt"))

	manager := NewManager(ctx)
	defer manager.Close()
	firstID, err := manager.Enqueue(Request{
		SourceFS: source, DestinationFS: destination, DestinationDir: destinationRoot,
		Entries: []filesystem.Entry{first},
	})
	if err != nil {
		t.Fatalf("Enqueue first: %v", err)
	}
	secondID, err := manager.Enqueue(Request{
		SourceFS: source, DestinationFS: destination, DestinationDir: destinationRoot,
		Entries: []filesystem.Entry{second},
	})
	if err != nil {
		t.Fatalf("Enqueue second: %v", err)
	}

	collected := make([]QueueEvent, 0, 8)
	terminal := make(map[uint64]Event, 2)
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for len(terminal) < 2 {
		select {
		case event := <-manager.Events():
			collected = append(collected, event)
			switch event.Event.Phase {
			case PhaseComplete, PhaseFailed, PhaseCanceled:
				terminal[event.RequestID] = event.Event
			}
		case <-timeout.C:
			t.Fatal("timed out waiting for queued transfers")
		}
	}
	if terminal[firstID].Phase != PhaseComplete || terminal[secondID].Phase != PhaseComplete {
		t.Fatalf("terminal events = %#v", terminal)
	}
	assertFileContents(t, filepath.Join(destinationRoot, "first.txt"), "first")
	assertFileContents(t, filepath.Join(destinationRoot, "second.txt"), "second")

	firstComplete := -1
	secondStarted := -1
	for index, event := range collected {
		if event.RequestID == firstID && event.Event.Phase == PhaseComplete {
			firstComplete = index
		}
		if event.RequestID == secondID && event.Event.Phase == PhasePlanning {
			secondStarted = index
		}
	}
	if firstComplete < 0 || secondStarted < 0 || firstComplete >= secondStarted {
		t.Fatalf("events were not sequential: first complete=%d second planning=%d", firstComplete, secondStarted)
	}
}

func TestManagerContinuesAfterFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	source := local.NewAt(sourceRoot)
	destination := local.NewAt(destinationRoot)
	writeTestFile(t, filepath.Join(sourceRoot, "good.txt"), "good")
	missing := filesystem.Entry{Name: "missing.txt", Path: filepath.Join(sourceRoot, "missing.txt"), Kind: filesystem.KindFile}
	good := statTestEntry(t, source, filepath.Join(sourceRoot, "good.txt"))

	manager := NewManager(ctx)
	defer manager.Close()
	missingID, err := manager.Enqueue(Request{
		SourceFS: source, DestinationFS: destination, DestinationDir: destinationRoot,
		Entries: []filesystem.Entry{missing},
	})
	if err != nil {
		t.Fatalf("Enqueue missing: %v", err)
	}
	goodID, err := manager.Enqueue(Request{
		SourceFS: source, DestinationFS: destination, DestinationDir: destinationRoot,
		Entries: []filesystem.Entry{good},
	})
	if err != nil {
		t.Fatalf("Enqueue good: %v", err)
	}

	terminal := waitForTerminalEvents(t, manager.Events(), 2)
	if terminal[missingID].Phase != PhaseFailed {
		t.Fatalf("missing request phase = %q, want failed", terminal[missingID].Phase)
	}
	if terminal[goodID].Phase != PhaseComplete {
		t.Fatalf("good request phase = %q, want complete", terminal[goodID].Phase)
	}
	assertFileContents(t, filepath.Join(destinationRoot, "good.txt"), "good")
}

func TestManagerCancelActive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	sourcePath := filepath.Join(sourceRoot, "blocked.txt")
	writeTestFile(t, sourcePath, "source")
	localSource := local.NewAt(sourceRoot)
	destination := local.NewAt(destinationRoot)
	entry := statTestEntry(t, localSource, sourcePath)
	blocking := &blockingOpenFS{FS: localSource, opened: make(chan struct{})}

	manager := NewManager(ctx)
	defer manager.Close()
	requestID, err := manager.Enqueue(Request{
		SourceFS: blocking, DestinationFS: destination, DestinationDir: destinationRoot,
		Entries: []filesystem.Entry{entry},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	select {
	case <-blocking.opened:
	case <-time.After(3 * time.Second):
		t.Fatal("transfer did not open the source")
	}
	manager.CancelActive()

	terminal := waitForTerminalEvents(t, manager.Events(), 1)
	if terminal[requestID].Phase != PhaseCanceled {
		t.Fatalf("terminal phase = %q, want canceled", terminal[requestID].Phase)
	}
	if _, err := destination.Stat(ctx, filepath.Join(destinationRoot, "blocked.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination error = %v, want not exist", err)
	}
}

func TestManagerRejectsInvalidRequest(t *testing.T) {
	t.Parallel()

	manager := NewManager(context.Background())
	defer manager.Close()
	if _, err := manager.Enqueue(Request{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Enqueue error = %v, want ErrInvalidRequest", err)
	}
}

func waitForTerminalEvents(t *testing.T, events <-chan QueueEvent, count int) map[uint64]Event {
	t.Helper()
	result := make(map[uint64]Event, count)
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for len(result) < count {
		select {
		case event := <-events:
			switch event.Event.Phase {
			case PhaseComplete, PhaseFailed, PhaseCanceled:
				result[event.RequestID] = event.Event
			}
		case <-timeout.C:
			t.Fatalf("timed out after %d terminal events", len(result))
		}
	}
	return result
}

func statTestEntry(t *testing.T, backend filesystem.FS, name string) filesystem.Entry {
	t.Helper()
	entry, err := backend.Stat(context.Background(), name)
	if err != nil {
		t.Fatalf("Stat(%q): %v", name, err)
	}
	return entry
}

type blockingOpenFS struct {
	filesystem.FS
	opened chan struct{}
	once   sync.Once
}

func (f *blockingOpenFS) Open(ctx context.Context, name string) (filesystem.Reader, error) {
	f.once.Do(func() { close(f.opened) })
	return &blockingReader{closed: make(chan struct{})}, nil
}

type blockingReader struct {
	closed chan struct{}
	once   sync.Once
}

func (r *blockingReader) Read([]byte) (int, error) {
	<-r.closed
	return 0, os.ErrClosed
}

func (r *blockingReader) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}
