package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rituu/sshut/internal/filesystem"
)

func TestPaneNavigationAndSelection(t *testing.T) {
	t.Parallel()

	pane := NewPane("LOCAL")
	pane.SetEntries([]filesystem.Entry{
		{Name: "a.txt", Path: "/tmp/a.txt", Kind: filesystem.KindFile},
		{Name: "b.txt", Path: "/tmp/b.txt", Kind: filesystem.KindFile},
		{Name: "src", Path: "/tmp/src", Kind: filesystem.KindDirectory},
	})

	pane.Move(1)
	if entry, _ := pane.Current(); entry.Name != "b.txt" {
		t.Fatalf("current entry = %q, want b.txt", entry.Name)
	}
	pane.ToggleSelected()
	selection := pane.Selection()
	if len(selection) != 1 || selection[0].Name != "b.txt" {
		t.Fatalf("selection = %#v, want b.txt", selection)
	}

	pane.Move(100)
	if pane.Cursor != len(pane.Entries)-1 {
		t.Fatalf("cursor = %d, want %d", pane.Cursor, len(pane.Entries)-1)
	}
	pane.ClearSelection()
	if selection = pane.Selection(); len(selection) != 1 || selection[0].Name != "src" {
		t.Fatalf("implicit selection = %#v, want src", selection)
	}
}

func TestPaneView(t *testing.T) {
	t.Parallel()

	modified := time.Date(2026, time.September, 25, 10, 30, 0, 0, time.UTC)
	pane := NewPane("LOCAL")
	pane.Path = "/tmp"
	pane.SetEntries([]filesystem.Entry{{
		Name:    "notes.txt",
		Path:    "/tmp/notes.txt",
		Kind:    filesystem.KindFile,
		Size:    1536,
		Mode:    0o644,
		ModTime: modified,
	}})

	view := pane.View(NewStyles(), 50, 10, true)
	for _, want := range []string{"LOCAL", "/tmp", "notes.txt", "1.5 KiB"} {
		if !contains(view, want) {
			t.Fatalf("view does not contain %q:\n%s", want, view)
		}
	}
}

func TestPaneViewKeepsCursorVisible(t *testing.T) {
	t.Parallel()

	entries := make([]filesystem.Entry, 20)
	for index := range entries {
		entries[index] = filesystem.Entry{
			Name: fmt.Sprintf("file-%02d.txt", index),
			Path: fmt.Sprintf("/tmp/file-%02d.txt", index),
			Kind: filesystem.KindFile,
		}
	}
	pane := NewPane("LOCAL")
	pane.SetEntries(entries)
	pane.Cursor = 15
	view := pane.View(NewStyles(), 50, 8, true)
	if !strings.Contains(view, "file-15.txt") {
		t.Fatalf("cursor entry is not visible:\n%s", view)
	}
	if strings.Contains(view, "file-00.txt") {
		t.Fatalf("viewport did not scroll past the first page:\n%s", view)
	}
}

func TestFormatSize(t *testing.T) {
	t.Parallel()

	tests := map[int64]string{
		12:                     "12 B",
		1024:                   "1.0 KiB",
		1024 * 1024:            "1.0 MiB",
		3 * 1024 * 1024 * 1024: "3.0 GiB",
	}
	for size, want := range tests {
		if got := formatSize(size); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", size, got, want)
		}
	}
}

func contains(value, substring string) bool {
	return strings.Contains(value, substring)
}
