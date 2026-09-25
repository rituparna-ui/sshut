package local

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalFilesystem(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	backend := NewAt(root)

	entries, err := backend.List(ctx, root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("empty directory has %d entries", len(entries))
	}

	if err := backend.Mkdir(ctx, filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	writer, err := backend.Create(ctx, filepath.Join(root, "hello.txt"), 0o644)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := writer.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	entries, err = backend.List(ctx, root)
	if err != nil {
		t.Fatalf("List after create: %v", err)
	}
	if len(entries) != 2 || entries[0].Name != "docs" || entries[0].Kind.String() != "directory" {
		t.Fatalf("unexpected entries: %#v", entries)
	}

	filePath := filepath.Join(root, "hello.txt")
	oldTime := time.Unix(1_700_000_000, 0)
	if err := backend.Chtimes(ctx, filePath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	info, err := backend.Stat(ctx, filePath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.ModTime.Equal(oldTime) {
		t.Fatalf("ModTime = %v, want %v", info.ModTime, oldTime)
	}

	if err := backend.Rename(ctx, filePath, filepath.Join(root, "renamed.txt")); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := backend.Rename(ctx, filepath.Join(root, "docs"), filepath.Join(root, "renamed.txt")); err == nil {
		t.Fatal("rename over existing destination unexpectedly succeeded")
	}

	linkPath := filepath.Join(root, "link")
	if err := backend.Symlink(ctx, "renamed.txt", linkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	entry, err := backend.Stat(ctx, linkPath)
	if err != nil {
		t.Fatalf("Lstat link: %v", err)
	}
	if entry.Kind.String() != "symlink" {
		t.Fatalf("link kind = %q, want symlink", entry.Kind)
	}

	if err := backend.RemoveAll(ctx, root); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := backend.Stat(ctx, root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat removed root error = %v, want fs.ErrNotExist", err)
	}
}
