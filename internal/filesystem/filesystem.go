// Package filesystem defines the storage operations shared by the local and
// remote file managers.
package filesystem

import (
	"context"
	"io"
	"os"
	"time"
)

// Kind describes the type of a filesystem entry without following symbolic
// links.
type Kind uint8

const (
	KindUnknown Kind = iota
	KindFile
	KindDirectory
	KindSymlink
	KindOther
)

// String returns a stable, human-readable kind name.
func (k Kind) String() string {
	switch k {
	case KindFile:
		return "file"
	case KindDirectory:
		return "directory"
	case KindSymlink:
		return "symlink"
	case KindOther:
		return "special"
	default:
		return "unknown"
	}
}

// Entry is the common metadata displayed and used by the file manager.
type Entry struct {
	Name    string
	Path    string
	Kind    Kind
	Size    int64
	Mode    os.FileMode
	ModTime time.Time
}

// IsDir reports whether the entry is a directory. Symbolic links are never
// followed implicitly.
func (e Entry) IsDir() bool { return e.Kind == KindDirectory }

// Reader is an open file being downloaded.
type Reader interface {
	io.Reader
	io.Closer
}

// Writer is an open file being uploaded.
type Writer interface {
	io.Writer
	io.Closer
}

// FS is the storage contract used by the UI and transfer engine. Path
// semantics are delegated to each implementation so local native paths and
// remote POSIX paths are handled correctly.
type FS interface {
	Label() string
	Home(context.Context) (string, error)
	List(context.Context, string) ([]Entry, error)
	Stat(context.Context, string) (Entry, error)
	Open(context.Context, string) (Reader, error)
	Create(context.Context, string, os.FileMode) (Writer, error)
	Mkdir(context.Context, string, os.FileMode) error
	MkdirAll(context.Context, string, os.FileMode) error
	Rename(context.Context, string, string) error
	Replace(context.Context, string, string) error
	RemoveAll(context.Context, string) error
	Readlink(context.Context, string) (string, error)
	Symlink(context.Context, string, string) error
	Chmod(context.Context, string, os.FileMode) error
	Chtimes(context.Context, string, time.Time, time.Time) error
	Resolve(context.Context, string) (string, error)
	Join(...string) string
	Parent(string) string
	Base(string) string
	Close() error
}
