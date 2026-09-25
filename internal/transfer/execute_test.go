package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rituu/sshut/internal/filesystem"
	"github.com/rituu/sshut/internal/local"
)

func TestExecuteCopiesDirectoryTree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	source := local.NewAt(sourceRoot)
	destination := local.NewAt(destinationRoot)
	writeTestFile(t, filepath.Join(sourceRoot, "project", "a.txt"), "abc")
	writeTestFile(t, filepath.Join(sourceRoot, "project", "nested", "b.txt"), "12345")
	entry, err := source.Stat(ctx, filepath.Join(sourceRoot, "project"))
	if err != nil {
		t.Fatalf("Stat source: %v", err)
	}
	plan, err := BuildPlan(ctx, source, destination, destinationRoot, []filesystem.Entry{entry}, nil)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	var events []Event
	if err := Execute(ctx, plan, source, destination, func(event Event) { events = append(events, event) }); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertFileContents(t, filepath.Join(destinationRoot, "project", "a.txt"), "abc")
	assertFileContents(t, filepath.Join(destinationRoot, "project", "nested", "b.txt"), "12345")
	if len(events) == 0 {
		t.Fatal("Execute emitted no events")
	}
	final := events[len(events)-1]
	if final.Phase != PhaseComplete || final.BytesTransferred != 8 || final.FilesCompleted != 2 {
		t.Fatalf("final event = %#v", final)
	}
	entries, err := destination.List(ctx, filepath.Join(destinationRoot, "project"))
	if err != nil {
		t.Fatalf("List destination: %v", err)
	}
	for _, copied := range entries {
		if filepath.Ext(copied.Name) == ".part" {
			t.Fatalf("temporary file remains: %s", copied.Path)
		}
	}
}

func TestExecuteMergesAndOverwritesConflicts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	source := local.NewAt(sourceRoot)
	destination := local.NewAt(destinationRoot)
	writeTestFile(t, filepath.Join(sourceRoot, "project", "report.txt"), "new")
	writeTestFile(t, filepath.Join(sourceRoot, "project", "docs", "manual.md"), "source")
	writeTestFile(t, filepath.Join(destinationRoot, "project", "report.txt"), "old")
	writeTestFile(t, filepath.Join(destinationRoot, "project", "docs", "existing.md"), "destination")
	writeTestFile(t, filepath.Join(destinationRoot, "project", "destination-only.txt"), "destination")
	entry, err := source.Stat(ctx, filepath.Join(sourceRoot, "project"))
	if err != nil {
		t.Fatalf("Stat source: %v", err)
	}
	plan, err := BuildPlan(ctx, source, destination, destinationRoot, []filesystem.Entry{entry}, ResolverFunc(
		func(context.Context, Conflict) (Decision, error) { return DecisionOverwrite, nil },
	))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if err := Execute(ctx, plan, source, destination, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	assertFileContents(t, filepath.Join(destinationRoot, "project", "report.txt"), "new")
	assertFileContents(t, filepath.Join(destinationRoot, "project", "docs", "manual.md"), "source")
	assertFileContents(t, filepath.Join(destinationRoot, "project", "docs", "existing.md"), "destination")
	assertFileContents(t, filepath.Join(destinationRoot, "project", "destination-only.txt"), "destination")
}

func TestExecuteOverwriteCanChangeEntryType(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	source := local.NewAt(sourceRoot)
	destination := local.NewAt(destinationRoot)
	sourceFile := filepath.Join(sourceRoot, "item")
	destinationFile := filepath.Join(destinationRoot, "item")
	writeTestFile(t, sourceFile, "source")
	writeTestFile(t, filepath.Join(destinationFile, "child"), "destination directory")
	entry, err := source.Stat(ctx, sourceFile)
	if err != nil {
		t.Fatalf("Stat source: %v", err)
	}
	plan, err := BuildPlan(ctx, source, destination, destinationRoot, []filesystem.Entry{entry}, ResolverFunc(
		func(context.Context, Conflict) (Decision, error) { return DecisionOverwrite, nil },
	))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if err := Execute(ctx, plan, source, destination, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertFileContents(t, destinationFile, "source")
}

func TestExecuteRecreatesSymbolicLink(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated privileges on Windows")
	}

	ctx := context.Background()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	source := local.NewAt(sourceRoot)
	destination := local.NewAt(destinationRoot)
	writeTestFile(t, filepath.Join(sourceRoot, "target.txt"), "target")
	linkPath := filepath.Join(sourceRoot, "link")
	if err := os.Symlink("target.txt", linkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	entry, err := source.Stat(ctx, linkPath)
	if err != nil {
		t.Fatalf("Stat link: %v", err)
	}
	plan, err := BuildPlan(ctx, source, destination, destinationRoot, []filesystem.Entry{entry}, nil)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if err := Execute(ctx, plan, source, destination, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	target, err := destination.Readlink(ctx, filepath.Join(destinationRoot, "link"))
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if target != "target.txt" {
		t.Fatalf("link target = %q, want target.txt", target)
	}
}

func TestExecuteCanceledBeforeDestinationChange(t *testing.T) {
	t.Parallel()

	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	writeTestFile(t, filepath.Join(sourceRoot, "file.txt"), "source")
	source := local.NewAt(sourceRoot)
	destination := local.NewAt(destinationRoot)
	entry, err := source.Stat(context.Background(), filepath.Join(sourceRoot, "file.txt"))
	if err != nil {
		t.Fatalf("Stat source: %v", err)
	}
	plan, err := BuildPlan(context.Background(), source, destination, destinationRoot, []filesystem.Entry{entry}, nil)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var final Event
	err = Execute(ctx, plan, source, destination, func(event Event) { final = event })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute error = %v, want context.Canceled", err)
	}
	if final.Phase != PhaseCanceled {
		t.Fatalf("final phase = %q, want canceled", final.Phase)
	}
	if _, err := destination.Stat(ctx, filepath.Join(destinationRoot, "file.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination error = %v, want not exist", err)
	}
}

func TestExecuteCleansTemporaryFileAfterWriteFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	writeTestFile(t, filepath.Join(sourceRoot, "file.txt"), "source")
	source := local.NewAt(sourceRoot)
	destination := local.NewAt(destinationRoot)
	entry, err := source.Stat(ctx, filepath.Join(sourceRoot, "file.txt"))
	if err != nil {
		t.Fatalf("Stat source: %v", err)
	}
	plan, err := BuildPlan(ctx, source, destination, destinationRoot, []filesystem.Entry{entry}, nil)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	failing := failingCreateFS{FS: destination}
	if err := Execute(ctx, plan, source, failing, nil); err == nil {
		t.Fatal("Execute unexpectedly succeeded")
	}
	entries, err := destination.List(ctx, destinationRoot)
	if err != nil {
		t.Fatalf("List destination: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("destination contains partial files: %#v", entries)
	}
}

type failingCreateFS struct {
	filesystem.FS
}

func (f failingCreateFS) Create(ctx context.Context, name string, mode os.FileMode) (filesystem.Writer, error) {
	writer, err := f.FS.Create(ctx, name, mode)
	if err != nil {
		return nil, err
	}
	return failingWriter{Writer: writer}, nil
}

type failingWriter struct {
	filesystem.Writer
}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("injected write failure")
}

func assertFileContents(t *testing.T, name, want string) {
	t.Helper()
	contents, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", name, err)
	}
	if string(contents) != want {
		t.Fatalf("contents of %q = %q, want %q", name, contents, want)
	}
}
