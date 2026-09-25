package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/rituu/sshut/internal/filesystem"
)

// Phase describes a transfer lifecycle event.
type Phase string

const (
	PhaseQueued       Phase = "queued"
	PhasePlanning     Phase = "planning"
	PhaseTransferring Phase = "transferring"
	PhaseComplete     Phase = "complete"
	PhaseSkipped      Phase = "skipped"
	PhaseCanceled     Phase = "canceled"
	PhaseFailed       Phase = "failed"
)

// Event is a point-in-time transfer update. Paths identify the current file;
// root event paths are empty.
type Event struct {
	Phase            Phase
	SourcePath       string
	DestinationPath  string
	CurrentPath      string
	BytesTransferred int64
	TotalBytes       int64
	FilesCompleted   int
	TotalFiles       int
	Err              error
}

// EmitFunc receives transfer events. It is called synchronously and should not
// block for long.
type EmitFunc func(Event)

const (
	copyBufferSize   = 128 << 10
	progressInterval = 100 * time.Millisecond
	cleanupTimeout   = 2 * time.Second
)

// Execute runs a previously built plan sequentially. The destination is never
// changed while conflicts are being planned.
func Execute(
	ctx context.Context,
	plan *Plan,
	sourceFS filesystem.FS,
	destinationFS filesystem.FS,
	emit EmitFunc,
) error {
	executor := &executor{
		source:       sourceFS,
		destination:  destinationFS,
		plan:         plan,
		emit:         emit,
		lastProgress: time.Now(),
	}
	err := executor.run(ctx)
	if err == nil {
		executor.send(Event{
			Phase:            PhaseComplete,
			BytesTransferred: executor.bytes,
			TotalBytes:       plan.TotalBytes,
			FilesCompleted:   executor.files,
			TotalFiles:       plan.TotalFiles,
		})
		return nil
	}

	phase := PhaseFailed
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		phase = PhaseCanceled
	}
	executor.send(Event{
		Phase:            phase,
		BytesTransferred: executor.bytes,
		TotalBytes:       plan.TotalBytes,
		FilesCompleted:   executor.files,
		TotalFiles:       plan.TotalFiles,
		Err:              err,
	})
	return err
}

type executor struct {
	source       filesystem.FS
	destination  filesystem.FS
	plan         *Plan
	emit         EmitFunc
	bytes        int64
	files        int
	lastProgress time.Time
}

func (e *executor) run(ctx context.Context) error {
	for _, root := range e.plan.Roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.executeNode(ctx, root); err != nil {
			return err
		}
	}
	return nil
}

func (e *executor) executeNode(ctx context.Context, node *Node) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if node.Action == ActionSkip {
		e.send(Event{
			Phase:            PhaseSkipped,
			SourcePath:       node.SourcePath,
			DestinationPath:  node.DestinationPath,
			CurrentPath:      node.SourcePath,
			BytesTransferred: e.bytes,
			TotalBytes:       e.plan.TotalBytes,
			FilesCompleted:   e.files,
			TotalFiles:       e.plan.TotalFiles,
		})
		return nil
	}

	if node.Source.IsDir() {
		return e.executeDirectory(ctx, node)
	}
	if node.Source.Kind == filesystem.KindSymlink {
		return e.executeSymlink(ctx, node)
	}
	return e.executeFile(ctx, node)
}

func (e *executor) executeDirectory(ctx context.Context, node *Node) error {
	e.sendProgress(node, false)
	if node.Action == ActionReplace {
		if err := e.destination.RemoveAll(ctx, node.DestinationPath); err != nil && !isNotExist(err) {
			return fmt.Errorf("replace directory %q: %w", node.DestinationPath, err)
		}
	}
	if err := e.destination.MkdirAll(ctx, node.DestinationPath, node.Source.Mode); err != nil {
		return fmt.Errorf("create directory %q: %w", node.DestinationPath, err)
	}

	for _, child := range node.Children {
		if err := e.executeNode(ctx, child); err != nil {
			return err
		}
	}
	if err := e.destination.Chmod(ctx, node.DestinationPath, node.Source.Mode); err != nil {
		return fmt.Errorf("set directory mode %q: %w", node.DestinationPath, err)
	}
	if err := e.destination.Chtimes(
		ctx,
		node.DestinationPath,
		node.Source.ModTime,
		node.Source.ModTime,
	); err != nil {
		return fmt.Errorf("set directory times %q: %w", node.DestinationPath, err)
	}
	e.sendProgress(node, true)
	return nil
}

func (e *executor) executeFile(ctx context.Context, node *Node) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.sendProgress(node, false)

	source, err := e.source.Open(ctx, node.SourcePath)
	if err != nil {
		return fmt.Errorf("open source %q: %w", node.SourcePath, err)
	}
	defer source.Close()

	temporary, err := e.temporaryPath(ctx, node.DestinationPath)
	if err != nil {
		return err
	}
	writer, err := e.destination.Create(ctx, temporary, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary destination %q: %w", temporary, err)
	}
	writerClosed := false
	defer func() {
		if !writerClosed {
			_ = writer.Close()
		}
		cleanup, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = e.destination.RemoveAll(cleanup, temporary)
	}()

	stopCancellation := context.AfterFunc(ctx, func() {
		_ = source.Close()
		_ = writer.Close()
	})
	_, copyErr := io.CopyBuffer(
		writer,
		&contextReader{
			ctx:    ctx,
			reader: source,
			onRead: func(count int) {
				e.bytes += int64(count)
				e.sendProgress(node, false)
			},
		},
		make([]byte, copyBufferSize),
	)
	stopCancellation()
	if err := ctx.Err(); err != nil {
		return err
	}
	if copyErr != nil {
		return fmt.Errorf("copy %q to %q: %w", node.SourcePath, node.DestinationPath, copyErr)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close temporary destination %q: %w", temporary, err)
	}
	writerClosed = true

	if err := e.destination.Chmod(ctx, temporary, node.Source.Mode); err != nil {
		return fmt.Errorf("set file mode %q: %w", temporary, err)
	}
	if err := e.destination.Chtimes(
		ctx,
		temporary,
		node.Source.ModTime,
		node.Source.ModTime,
	); err != nil {
		return fmt.Errorf("set file times %q: %w", temporary, err)
	}
	if err := e.commit(ctx, temporary, node); err != nil {
		return err
	}

	e.files++
	e.sendProgress(node, true)
	return nil
}

func (e *executor) executeSymlink(ctx context.Context, node *Node) error {
	target, err := e.source.Readlink(ctx, node.SourcePath)
	if err != nil {
		return fmt.Errorf("read symbolic link %q: %w", node.SourcePath, err)
	}
	temporary, err := e.temporaryPath(ctx, node.DestinationPath)
	if err != nil {
		return err
	}
	if err := e.destination.Symlink(ctx, target, temporary); err != nil {
		return fmt.Errorf("create symbolic link %q: %w", temporary, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = e.destination.RemoveAll(cleanup, temporary)
	}()
	if err := e.commit(ctx, temporary, node); err != nil {
		return err
	}
	e.files++
	e.sendProgress(node, true)
	return nil
}

func (e *executor) commit(ctx context.Context, temporary string, node *Node) error {
	if node.Action == ActionReplace {
		if err := e.destination.Replace(ctx, temporary, node.DestinationPath); err != nil {
			return fmt.Errorf("replace destination %q: %w", node.DestinationPath, err)
		}
		return nil
	}
	if err := e.destination.Rename(ctx, temporary, node.DestinationPath); err != nil {
		return fmt.Errorf("commit destination %q: %w", node.DestinationPath, err)
	}
	return nil
}

func (e *executor) temporaryPath(ctx context.Context, destination string) (string, error) {
	dir := e.destination.Parent(destination)
	for attempts := 0; attempts < 10; attempts++ {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", fmt.Errorf("create temporary name: %w", err)
		}
		candidate := e.destination.Join(dir, ".sshut-"+hex.EncodeToString(random[:])+".part")
		_, err := e.destination.Stat(ctx, candidate)
		if isNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("check temporary name: %w", err)
		}
	}
	return "", fmt.Errorf("could not allocate a temporary name in %q", dir)
}

func (e *executor) sendProgress(node *Node, force bool) {
	now := time.Now()
	if !force && now.Sub(e.lastProgress) < progressInterval {
		return
	}
	e.lastProgress = now
	e.send(Event{
		Phase:            PhaseTransferring,
		SourcePath:       node.SourcePath,
		DestinationPath:  node.DestinationPath,
		CurrentPath:      node.SourcePath,
		BytesTransferred: e.bytes,
		TotalBytes:       e.plan.TotalBytes,
		FilesCompleted:   e.files,
		TotalFiles:       e.plan.TotalFiles,
	})
}

func (e *executor) send(event Event) {
	if e.emit != nil {
		e.emit(event)
	}
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
	onRead func(int)
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	count, err := r.reader.Read(buffer)
	if count > 0 && r.onRead != nil {
		r.onRead(count)
	}
	return count, err
}
