package sftp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSystemSFTPServer exercises the same SFTP protocol path used after the
// system ssh process connects. It is opt-in because it launches a local
// OpenSSH sftp-server and is not appropriate for every unit-test environment.
func TestSystemSFTPServer(t *testing.T) {
	if os.Getenv("SSHUT_SFTP_INTEGRATION") != "1" {
		t.Skip("set SSHUT_SFTP_INTEGRATION=1 to run the system sftp-server test")
	}
	server := os.Getenv("SSHUT_SFTP_SERVER")
	if server == "" {
		server = "/usr/libexec/sftp-server"
	}
	if _, err := os.Stat(server); err != nil {
		t.Skipf("sftp-server is unavailable: %v", err)
	}

	script := filepath.Join(t.TempDir(), "ssh")
	contents := "#!/bin/sh\nexec " + server + "\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	backend, err := connect(ctx, "local-test", script)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer backend.Close()

	home, err := backend.Home(ctx)
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	name := filepath.Join(home, ".sshut-integration-test")
	writer, err := backend.Create(ctx, name, 0o600)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := writer.Write([]byte("sftp integration")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	contentsRead, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read local result: %v", err)
	}
	if string(contentsRead) != "sftp integration" {
		t.Fatalf("result = %q", contentsRead)
	}
	if err := backend.RemoveAll(ctx, name); err != nil {
		t.Fatalf("remove: %v", err)
	}
}
