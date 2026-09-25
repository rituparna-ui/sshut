// Package sftp implements filesystem.FS using the system OpenSSH client and
// an SFTP subsystem.
package sftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	pkgsftp "github.com/pkg/sftp"

	"github.com/rituu/sshut/internal/filesystem"
)

const (
	handshakeBufferSize = 32 << 10
	diagnosticLimit     = 8 << 10
	shutdownTimeout     = 2 * time.Second
)

// FS is a remote filesystem backed by one SFTP session.
type FS struct {
	destination string
	client      *pkgsftp.Client
	command     *exec.Cmd
	stdin       io.WriteCloser
	diagnostics *tailBuffer

	closeOnce sync.Once
	closeErr  error
}

var _ filesystem.FS = (*FS)(nil)

// Connect opens an SFTP subsystem through the system ssh command. It inherits
// the user's OpenSSH configuration, agent, host keys, and ProxyJump settings.
func Connect(ctx context.Context, destination string) (*FS, error) {
	sshBinary, err := exec.LookPath("ssh")
	if err != nil {
		return nil, fmt.Errorf("system ssh client not found: %w", err)
	}
	return connect(ctx, destination, sshBinary)
}

func connect(ctx context.Context, destination, sshBinary string) (*FS, error) {
	if strings.TrimSpace(destination) == "" {
		return nil, errors.New("ssh destination cannot be empty")
	}

	command := exec.Command(sshBinary, sshArgs(destination)...)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open ssh stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open ssh stdout: %w", err)
	}
	diagnostics := &tailBuffer{limit: diagnosticLimit}
	command.Stderr = diagnostics

	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start ssh: %w", err)
	}

	type clientResult struct {
		client *pkgsftp.Client
		err    error
	}
	result := make(chan clientResult, 1)
	go func() {
		client, err := pkgsftp.NewClientPipe(stdout, stdin)
		result <- clientResult{client: client, err: err}
	}()

	select {
	case connected := <-result:
		if connected.err != nil {
			_ = stopCommand(command, stdin)
			return nil, connectionError(destination, diagnostics, connected.err)
		}
		return &FS{
			destination: destination,
			client:      connected.client,
			command:     command,
			stdin:       stdin,
			diagnostics: diagnostics,
		}, nil
	case <-ctx.Done():
		_ = stopCommand(command, stdin)
		return nil, connectionError(destination, diagnostics, ctx.Err())
	}
}

func sshArgs(destination string) []string {
	return []string{"-T", "-o", "BatchMode=yes", "-s", destination, "sftp"}
}

func connectionError(destination string, diagnostics *tailBuffer, cause error) error {
	message := strings.TrimSpace(diagnostics.String())
	if message == "" {
		return fmt.Errorf("connect to %q over SFTP: %w", destination, cause)
	}
	return fmt.Errorf("connect to %q over SFTP: %w: %s", destination, cause, message)
}

func stopCommand(command *exec.Cmd, stdin io.WriteCloser) error {
	_ = stdin.Close()
	if command.Process != nil {
		_ = command.Process.Kill()
	}
	return command.Wait()
}

// Label identifies the remote side in the interface.
func (f *FS) Label() string { return "remote:" + f.destination }

// Home returns the remote user's starting directory.
func (f *FS) Home(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	home, err := f.client.Getwd()
	if err == nil {
		return home, nil
	}
	home, fallbackErr := f.client.RealPath(".")
	if fallbackErr != nil {
		return "", fmt.Errorf("resolve remote home: %w", err)
	}
	return home, nil
}

// List returns remote entries without following symbolic links.
func (f *FS) List(ctx context.Context, dir string) ([]filesystem.Entry, error) {
	items, err := f.client.ReadDirContext(ctx, dir)
	if err != nil {
		return nil, err
	}
	entries := make([]filesystem.Entry, 0, len(items))
	for _, item := range items {
		entries = append(entries, filesystem.EntryFromInfo(path.Join(dir, item.Name()), item))
	}
	filesystem.Sort(entries)
	return entries, nil
}

// Stat returns remote metadata without following a final symbolic link.
func (f *FS) Stat(ctx context.Context, name string) (filesystem.Entry, error) {
	if err := ctx.Err(); err != nil {
		return filesystem.Entry{}, err
	}
	info, err := f.client.Lstat(name)
	if err != nil {
		return filesystem.Entry{}, err
	}
	return filesystem.EntryFromInfo(name, info), nil
}

// Open opens a remote file for reading.
func (f *FS) Open(ctx context.Context, name string) (filesystem.Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.client.Open(name)
}

// Create exclusively creates a remote file for writing.
func (f *FS) Create(ctx context.Context, name string, mode os.FileMode) (filesystem.Writer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.client.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
}

// Mkdir creates one remote directory.
func (f *FS) Mkdir(ctx context.Context, name string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.client.Mkdir(name); err != nil {
		return err
	}
	if err := f.client.Chmod(name, mode); err != nil {
		_ = f.client.RemoveDirectory(name)
		return fmt.Errorf("set directory mode: %w", err)
	}
	return nil
}

// MkdirAll creates a remote directory and any missing parents.
func (f *FS) MkdirAll(ctx context.Context, name string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.client.MkdirAll(name); err != nil {
		return err
	}
	if err := f.client.Chmod(name, mode); err != nil {
		return fmt.Errorf("set directory mode: %w", err)
	}
	return nil
}

// Rename renames a remote entry after checking that the target is unused.
func (f *FS) Rename(ctx context.Context, oldName, newName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := f.client.Lstat(newName); err == nil {
		return fmt.Errorf("destination already exists: %s", newName)
	} else if !os.IsNotExist(err) {
		return err
	}
	return f.client.Rename(oldName, newName)
}

// RemoveAll recursively removes a remote entry without following a final
// symbolic link.
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := f.client.Lstat(name)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return f.client.Remove(name)
	}

	items, err := f.client.ReadDirContext(ctx, name)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := f.RemoveAll(ctx, path.Join(name, item.Name())); err != nil {
			return err
		}
	}
	return f.client.RemoveDirectory(name)
}

// Readlink returns a remote symbolic link target.
func (f *FS) Readlink(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return f.client.ReadLink(name)
}

// Symlink creates target at name on the remote filesystem.
func (f *FS) Symlink(ctx context.Context, target, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.client.Symlink(target, name)
}

// Chmod changes remote permissions.
func (f *FS) Chmod(ctx context.Context, name string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.client.Chmod(name, mode)
}

// Chtimes changes remote access and modification times.
func (f *FS) Chtimes(ctx context.Context, name string, atime, mtime time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.client.Chtimes(name, atime, mtime)
}

// Resolve asks the server for a canonical remote path.
func (f *FS) Resolve(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if name == "" {
		name = "."
	}
	return f.client.RealPath(name)
}

// Join joins remote POSIX path elements.
func (*FS) Join(parts ...string) string { return path.Join(parts...) }

// Parent returns the parent remote POSIX directory.
func (*FS) Parent(name string) string { return path.Dir(name) }

// Base returns the final remote POSIX path element.
func (*FS) Base(name string) string { return path.Base(name) }

// Close ends the SFTP session and reaps the ssh process.
func (f *FS) Close() error {
	f.closeOnce.Do(func() {
		if f.stdin != nil {
			_ = f.stdin.Close()
		}
		if f.client != nil {
			if err := f.client.Close(); err != nil && !errors.Is(err, io.EOF) {
				f.closeErr = err
			}
		}
		if f.command == nil || f.command.Process == nil {
			return
		}

		wait := make(chan error, 1)
		go func() { wait <- f.command.Wait() }()
		select {
		case err := <-wait:
			if err != nil && f.closeErr == nil {
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) || exitError.ExitCode() != -1 {
					f.closeErr = err
				}
			}
		case <-time.After(shutdownTimeout):
			_ = f.command.Process.Kill()
			<-wait
			if f.closeErr == nil {
				f.closeErr = errors.New("timed out closing remote SFTP session")
			}
		}
	})
	return f.closeErr
}

// tailBuffer retains only the most recent diagnostic output.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	written := len(p)
	if b.limit <= 0 {
		return written, nil
	}
	if len(p) >= b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
		return written, nil
	}
	if overflow := len(b.data) + len(p) - b.limit; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, p...)
	return written, nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
