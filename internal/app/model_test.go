package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestFileMutationsCreateRenameAndDelete(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	model := loadedLocalModel(t, root)

	updated, _ := model.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	model = updated.(Model)
	if model.modal.kind != modalNewDirectory {
		t.Fatalf("modal kind = %v, want new directory", model.modal.kind)
	}
	model.modal.input.SetValue("created")
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	result := command().(mutationDoneMsg)
	if result.err != nil {
		t.Fatalf("create directory: %v", result.err)
	}
	model = applyRefresh(t, model, result)

	created := filepath.Join(root, "created")
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("created directory: %v", err)
	}
	focusEntry(t, &model, "created")
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	model = updated.(Model)
	model.modal.input.SetValue("renamed")
	updated, command = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	result = command().(mutationDoneMsg)
	if result.err != nil {
		t.Fatalf("rename: %v", result.err)
	}
	model = applyRefresh(t, model, result)

	renamed := filepath.Join(root, "renamed")
	if _, err := os.Stat(renamed); err != nil {
		t.Fatalf("renamed directory: %v", err)
	}
	focusEntry(t, &model, "renamed")
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	model = updated.(Model)
	if model.modal.kind != modalDelete || len(model.modal.entries) != 1 {
		t.Fatalf("delete modal = %#v", model.modal)
	}
	updated, command = model.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	model = updated.(Model)
	result = command().(mutationDoneMsg)
	if result.err != nil {
		t.Fatalf("delete: %v", result.err)
	}
	model = applyRefresh(t, model, result)
	if _, err := os.Stat(renamed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted directory error = %v, want not exist", err)
	}
}

func TestGoToPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	model := loadedLocalModel(t, root)
	updated, _ := model.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	model = updated.(Model)
	model.modal.input.SetValue("nested")
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	resolved := command().(pathResolvedMsg)
	if resolved.err != nil {
		t.Fatalf("resolve path: %v", resolved.err)
	}
	updated, command = model.Update(resolved)
	model = updated.(Model)
	loaded := command().(directoryLoadedMsg)
	if loaded.err != nil {
		t.Fatalf("load resolved path: %v", loaded.err)
	}
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	if model.local.Path != filepath.Join(root, "nested") {
		t.Fatalf("local path = %q, want nested", model.local.Path)
	}
}

func loadedLocalModel(t *testing.T, root string) Model {
	t.Helper()
	backend := local.NewAt(root)
	model := NewLocal()
	model.localFS = backend
	model.local.Path = root
	entries, err := backend.List(context.Background(), root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	model.local.SetEntries(withParent(backend, root, entries))
	return model
}

func applyRefresh(t *testing.T, model Model, result mutationDoneMsg) Model {
	t.Helper()
	updated, command := model.Update(result)
	model = updated.(Model)
	if command == nil {
		t.Fatal("mutation returned no refresh command")
	}
	loaded := command().(directoryLoadedMsg)
	if loaded.err != nil {
		t.Fatalf("refresh: %v", loaded.err)
	}
	updated, _ = model.Update(loaded)
	return updated.(Model)
}

func focusEntry(t *testing.T, model *Model, name string) {
	t.Helper()
	for index, entry := range model.local.Entries {
		if entry.Name == name {
			model.local.Cursor = index
			return
		}
	}
	t.Fatalf("entry %q not found in %#v", name, model.local.Entries)
}

func TestConnectionPromptStartsRemote(t *testing.T) {
	t.Parallel()

	model := New("")
	if !model.prompting {
		t.Fatal("New(\"\") did not open the connection prompt")
	}
	view := model.View().Content
	for _, want := range []string{"sshut", "SSH destination", "enter connect"} {
		if !strings.Contains(view, want) {
			t.Fatalf("prompt does not contain %q:\n%s", want, view)
		}
	}

	model.connectInput.SetValue("production")
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	if model.prompting || model.destination != "production" {
		t.Fatalf("prompt state = prompting:%v destination:%q", model.prompting, model.destination)
	}
	if !model.remote.Loading {
		t.Fatal("remote pane was not put into loading state")
	}
	if command == nil {
		t.Fatal("submitting destination returned no connect command")
	}
}

func TestModelRemotePaneAndFocus(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "remote.txt"), []byte("remote"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	remoteBackend := local.NewAt(root)
	model := New("production")
	if !model.remote.Loading {
		t.Fatal("remote pane did not start in loading state")
	}

	updated, listCommand := model.Update(remoteConnectedMsg{backend: remoteBackend, path: root})
	model = updated.(Model)
	if listCommand == nil {
		t.Fatal("remote connection returned no list command")
	}
	updated, _ = model.Update(listCommand().(directoryLoadedMsg))
	model = updated.(Model)
	if model.remote.Path != root || len(model.remote.Entries) != 2 {
		t.Fatalf("remote pane was not loaded: path=%q entries=%#v", model.remote.Path, model.remote.Entries)
	}

	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model = updated.(Model)
	if model.active != remoteSide {
		t.Fatalf("active side = %v, want remote", model.active)
	}
	view := model.View().Content
	for _, want := range []string{"LOCAL", "REMOTE production", "remote.txt"} {
		if !strings.Contains(view, want) {
			t.Fatalf("split view does not contain %q:\n%s", want, view)
		}
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
