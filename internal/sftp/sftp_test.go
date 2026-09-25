package sftp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSSHArgs(t *testing.T) {
	t.Parallel()

	got := sshArgs("production")
	want := []string{"-T", "-o", "BatchMode=yes", "-s", "production", "sftp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sshArgs() = %#v, want %#v", got, want)
	}
}

func TestTailBuffer(t *testing.T) {
	t.Parallel()

	buffer := &tailBuffer{limit: 5}
	if _, err := buffer.Write([]byte("123456789")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buffer.String(); got != "56789" {
		t.Fatalf("String() = %q, want %q", got, "56789")
	}
}

func TestConnectRejectsEmptyDestination(t *testing.T) {
	t.Parallel()

	if _, err := connect(context.Background(), "  ", "ssh"); err == nil {
		t.Fatal("connect unexpectedly accepted an empty destination")
	}
}

func TestConnectIncludesSSHDiagnostics(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "ssh")
	contents := "#!/bin/sh\nprintf '%s\\n' 'useful ssh diagnostic' >&2\nexit 255\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}

	_, err := connect(context.Background(), "production", script)
	if err == nil {
		t.Fatal("connect unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "useful ssh diagnostic") {
		t.Fatalf("connect error %q does not include SSH diagnostics", err)
	}
}

func TestRemotePathOperations(t *testing.T) {
	t.Parallel()

	backend := &FS{}
	if got := backend.Join("/srv", "app", "..", "logs"); got != "/srv/logs" {
		t.Fatalf("Join() = %q, want /srv/logs", got)
	}
	if got := backend.Parent("/srv/logs"); got != "/srv" {
		t.Fatalf("Parent() = %q, want /srv", got)
	}
	if got := backend.Parent("/"); got != "/" {
		t.Fatalf("Parent(/) = %q, want /", got)
	}
	if got := backend.Base("/srv/logs"); got != "logs" {
		t.Fatalf("Base() = %q, want logs", got)
	}
}
