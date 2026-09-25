package app

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rituu/sshut/internal/filesystem"
	"github.com/rituu/sshut/internal/local"
)

func TestModelLoadsLocalHome(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "documents"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	model := NewLocal()
	model.localFS = local.NewAt(root)

	homeCommand := model.Init()
	if homeCommand == nil {
		t.Fatal("Init returned nil")
	}
	homeResult := homeCommand()
	homeMessage, ok := homeResult.(homeLoadedMsg)
	if !ok {
		t.Fatalf("Init message = %T, want homeLoadedMsg", homeResult)
	}
	if homeMessage.err != nil {
		t.Fatalf("load home: %v", homeMessage.err)
	}

	updated, listCommand := model.Update(homeMessage)
	model = updated.(Model)
	if listCommand == nil {
		t.Fatal("home update returned no directory command")
	}
	directoryResult := listCommand()
	directoryMessage, ok := directoryResult.(directoryLoadedMsg)
	if !ok {
		t.Fatalf("directory message = %T, want directoryLoadedMsg", directoryResult)
	}
	updated, _ = model.Update(directoryMessage)
	model = updated.(Model)

	if model.local.Path != root {
		t.Fatalf("local path = %q, want %q", model.local.Path, root)
	}
	if len(model.local.Entries) != 3 {
		t.Fatalf("entry count = %d, want 3 (including parent)", len(model.local.Entries))
	}
	if model.local.Entries[0].Name != ".." || model.local.Entries[1].Name != "documents" {
		t.Fatalf("unexpected entries: %#v", model.local.Entries)
	}
}

func TestModelNavigation(t *testing.T) {
	t.Parallel()

	model := NewLocal()
	model.local.Path = "/tmp"
	model.local.SetEntries([]filesystem.Entry{
		{Name: "..", Path: "/", Kind: filesystem.KindDirectory},
		{Name: "file.txt", Path: "/tmp/file.txt", Kind: filesystem.KindFile},
	})

	updated, command := model.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	model = updated.(Model)
	if model.local.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", model.local.Cursor)
	}
	if command != nil {
		t.Fatal("moving emitted an unexpected command")
	}
}
