package app

import (
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
