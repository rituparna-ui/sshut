// Package local implements filesystem.FS for the local machine.
package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rituu/sshut/internal/filesystem"
)

// FS provides access to the local filesystem.
type FS struct {
	start string
}

// New returns a local filesystem rooted at the process working directory.
func New() *FS { return &FS{} }

// NewAt returns a local filesystem with an explicit initial directory.
func NewAt(start string) *FS { return &FS{start: start} }

// Label identifies this side in the interface.
func (*FS) Label() string { return "local" }

// Home returns the initial local directory.
func (f *FS) Home(ctx context.Context) (string, error) {
	if f.start == "" {
		return f.Resolve(ctx, ".")
	}
	return f.Resolve(ctx, f.start)
}

// List returns directory entries without following symbolic links.
func (f *FS) List(ctx context.Context, dir string) ([]filesystem.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]filesystem.Entry, 0, len(items))
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			return nil, fmt.Errorf("stat %q: %w", item.Name(), err)
		}
		entries = append(entries, filesystem.EntryFromInfo(filepath.Join(dir, item.Name()), info))
	}
	filesystem.Sort(entries)
	return entries, nil
}

// Stat returns metadata for path without following a final symbolic link.
func (*FS) Stat(_ context.Context, name string) (filesystem.Entry, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return filesystem.Entry{}, err
	}
	return filesystem.EntryFromInfo(name, info), nil
}

// Open opens a local file for reading.
func (*FS) Open(_ context.Context, name string) (filesystem.Reader, error) {
	return os.Open(name)
}

// Create exclusively creates a local file for writing.
func (*FS) Create(_ context.Context, name string, mode os.FileMode) (filesystem.Writer, error) {
	return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
}

// Mkdir creates one local directory.
func (*FS) Mkdir(_ context.Context, name string, mode os.FileMode) error {
	return os.Mkdir(name, mode.Perm())
}

// MkdirAll creates a local directory and any missing parents.
func (*FS) MkdirAll(_ context.Context, name string, mode os.FileMode) error {
	return os.MkdirAll(name, mode.Perm())
}

// Rename renames a local entry after checking that the target is unused.
func (*FS) Rename(_ context.Context, oldName, newName string) error {
	if _, err := os.Lstat(newName); err == nil {
		return fmt.Errorf("destination already exists: %s", newName)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(oldName, newName)
}

// RemoveAll recursively removes a local entry.
func (*FS) RemoveAll(_ context.Context, name string) error {
	return os.RemoveAll(name)
}

// Readlink returns a symbolic link target.
func (*FS) Readlink(_ context.Context, name string) (string, error) {
	return os.Readlink(name)
}

// Symlink creates target at name.
func (*FS) Symlink(_ context.Context, target, name string) error {
	return os.Symlink(target, name)
}

// Chmod changes local permissions.
func (*FS) Chmod(_ context.Context, name string, mode os.FileMode) error {
	return os.Chmod(name, mode.Perm())
}

// Chtimes changes local access and modification times.
func (*FS) Chtimes(_ context.Context, name string, atime, mtime time.Time) error {
	return os.Chtimes(name, atime, mtime)
}

// Resolve makes a local path absolute without dereferencing symbolic links.
func (*FS) Resolve(_ context.Context, name string) (string, error) {
	if name == "" {
		name = "."
	}
	return filepath.Abs(name)
}

// Join joins local path elements.
func (*FS) Join(parts ...string) string { return filepath.Join(parts...) }

// Parent returns the parent local directory.
func (*FS) Parent(name string) string { return filepath.Dir(name) }

// Base returns the final local path element.
func (*FS) Base(name string) string { return filepath.Base(name) }

// Close releases local resources. It is safe to call more than once.
func (*FS) Close() error { return nil }
