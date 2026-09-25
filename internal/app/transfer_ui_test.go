package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rituu/sshut/internal/local"
	"github.com/rituu/sshut/internal/transfer"
)

func TestDirectionalTransfers(t *testing.T) {
	t.Parallel()

	t.Run("upload", func(t *testing.T) {
		t.Parallel()
		localRoot := t.TempDir()
		remoteRoot := t.TempDir()
		writeAppTestFile(t, filepath.Join(localRoot, "upload.txt"), "upload")
		model := twoPaneModel(t, localRoot, remoteRoot)
		defer func() { _ = model.Close() }()

		updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		model = updated.(Model)
		model = driveTransfer(t, model, command, false)
		assertAppFile(t, filepath.Join(remoteRoot, "upload.txt"), "upload")
		if model.transferState.Phase != transfer.PhaseComplete {
			t.Fatalf("transfer phase = %q, want complete", model.transferState.Phase)
		}
	})

	t.Run("download", func(t *testing.T) {
		t.Parallel()
		localRoot := t.TempDir()
		remoteRoot := t.TempDir()
		writeAppTestFile(t, filepath.Join(remoteRoot, "download.txt"), "download")
		model := twoPaneModel(t, localRoot, remoteRoot)
		defer func() { _ = model.Close() }()
		updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		model = updated.(Model)
		model = driveTransfer(t, model, command, false)
		assertAppFile(t, filepath.Join(localRoot, "download.txt"), "download")
	})
}

func TestTransferConflictKeepBoth(t *testing.T) {
	t.Parallel()

	localRoot := t.TempDir()
	remoteRoot := t.TempDir()
	writeAppTestFile(t, filepath.Join(localRoot, "report.txt"), "source")
	writeAppTestFile(t, filepath.Join(remoteRoot, "report.txt"), "destination")
	model := twoPaneModel(t, localRoot, remoteRoot)
	defer func() { _ = model.Close() }()

	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	model = updated.(Model)
	model = driveTransfer(t, model, command, true)
	assertAppFile(t, filepath.Join(remoteRoot, "report.txt"), "destination")
	assertAppFile(t, filepath.Join(remoteRoot, "report (1).txt"), "source")
}

func TestTransferLineShowsProgress(t *testing.T) {
	t.Parallel()

	model := NewLocal()
	defer func() { _ = model.Close() }()
	model.transferState = transferViewState{
		Active:           true,
		Phase:            transfer.PhaseTransferring,
		SourceLabel:      "local",
		DestinationLabel: "remote:prod",
		Bytes:            5 << 20,
		TotalBytes:       10 << 20,
		StartedAt:        time.Now().Add(-2 * time.Second),
		UpdatedAt:        time.Now(),
	}
	line := model.transferLine(120)
	for _, want := range []string{"local", "remote:prod", "5.0 MiB", "10.0 MiB"} {
		if !strings.Contains(line, want) {
			t.Fatalf("transfer line does not contain %q: %s", want, line)
		}
	}
}

func twoPaneModel(t *testing.T, localRoot, remoteRoot string) Model {
	t.Helper()
	model := New("production")
	model.localFS = local.NewAt(localRoot)
	model.remoteFS = local.NewAt(remoteRoot)
	model.local.Path = localRoot
	model.remote.Path = remoteRoot
	localEntries, err := model.localFS.List(context.Background(), localRoot)
	if err != nil {
		t.Fatalf("List local: %v", err)
	}
	remoteEntries, err := model.remoteFS.List(context.Background(), remoteRoot)
	if err != nil {
		t.Fatalf("List remote: %v", err)
	}
	model.local.SetEntries(withParent(model.localFS, localRoot, localEntries))
	model.remote.SetEntries(withParent(model.remoteFS, remoteRoot, remoteEntries))
	if len(model.local.Entries) > 1 {
		model.local.Cursor = 1
	}
	if len(model.remote.Entries) > 1 {
		model.remote.Cursor = 1
	}
	model.local.Loading = false
	model.remote.Loading = false
	return model
}

func driveTransfer(t *testing.T, model Model, command tea.Cmd, answerConflict bool) Model {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	for steps := 0; steps < 30; steps++ {
		if command == nil {
			t.Fatal("transfer activity reader stopped unexpectedly")
		}
		resultChannel := make(chan tea.Msg, 1)
		go func() { resultChannel <- command() }()
		var result tea.Msg
		select {
		case result = <-resultChannel:
		case <-deadline.C:
			t.Fatalf("timed out waiting for transfer activity at step %d (phase=%q conflict=%v)", steps, model.transferState.Phase, model.pendingConflict != nil)
		}
		activity, ok := result.(transferActivityMsg)
		if !ok {
			t.Fatalf("activity message = %T, want transferActivityMsg", result)
		}
		if activity.done {
			t.Fatal("transfer manager closed during test")
		}
		updated, next := model.Update(activity)
		model = updated.(Model)

		if activity.conflict != nil {
			if !answerConflict {
				t.Fatal("unexpected transfer conflict")
			}
			if model.modal.kind != modalConflict {
				t.Fatalf("modal kind = %v, want conflict", model.modal.kind)
			}
			updated, _ = model.Update(tea.KeyPressMsg{Code: 'K', Text: "K"})
			model = updated.(Model)
			if model.pendingConflict != nil {
				t.Fatal("keep-both key did not answer conflict")
			}
			command = next
			continue
		}

		if terminalTransferPhase(activity.event.Event.Phase) {
			return model
		}
		command = next
		select {
		case <-deadline.C:
			t.Fatal("timed out driving transfer")
		default:
		}
	}
	t.Fatal("transfer did not finish")
	return model
}

func writeAppTestFile(t *testing.T, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(name, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func assertAppFile(t *testing.T, name, contents string) {
	t.Helper()
	actual, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", name, err)
	}
	if string(actual) != contents {
		t.Fatalf("contents of %q = %q, want %q", name, actual, contents)
	}
}
