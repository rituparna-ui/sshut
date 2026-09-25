package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/rituu/sshut/internal/filesystem"
)

type mutationKind string

const (
	mutationCreateDirectory mutationKind = "create directory"
	mutationRename          mutationKind = "rename"
	mutationDelete          mutationKind = "delete"
)

type mutationDoneMsg struct {
	side        side
	kind        mutationKind
	refreshPath string
	err         error
}

type pathResolvedMsg struct {
	side side
	path string
	err  error
}

func createDirectory(which side, backend filesystem.FS, directory, name string) tea.Cmd {
	path := backend.Join(directory, name)
	return func() tea.Msg {
		err := backend.Mkdir(context.Background(), path, 0o755)
		return mutationDoneMsg{
			side:        which,
			kind:        mutationCreateDirectory,
			refreshPath: directory,
			err:         err,
		}
	}
}

func renameEntry(which side, backend filesystem.FS, oldPath, newPath string) tea.Cmd {
	return func() tea.Msg {
		err := backend.Rename(context.Background(), oldPath, newPath)
		return mutationDoneMsg{
			side:        which,
			kind:        mutationRename,
			refreshPath: backend.Parent(oldPath),
			err:         err,
		}
	}
}

func deleteEntries(which side, backend filesystem.FS, directory string, entries []filesystem.Entry) tea.Cmd {
	return func() tea.Msg {
		var errs []error
		for _, entry := range entries {
			if entry.Name == ".." || backend.Parent(entry.Path) == entry.Path {
				errs = append(errs, fmt.Errorf("refusing to delete filesystem root %q", entry.Path))
				continue
			}
			if err := backend.RemoveAll(context.Background(), entry.Path); err != nil {
				errs = append(errs, fmt.Errorf("delete %q: %w", entry.Path, err))
			}
		}
		return mutationDoneMsg{
			side:        which,
			kind:        mutationDelete,
			refreshPath: directory,
			err:         errors.Join(errs...),
		}
	}
}

func resolveDirectory(which side, backend filesystem.FS, requested string) tea.Cmd {
	return func() tea.Msg {
		resolved, err := backend.Resolve(context.Background(), requested)
		if err != nil {
			return pathResolvedMsg{side: which, err: err}
		}
		entry, err := backend.Stat(context.Background(), resolved)
		if err != nil {
			return pathResolvedMsg{side: which, err: err}
		}
		if !entry.IsDir() {
			return pathResolvedMsg{side: which, err: fmt.Errorf("not a directory: %s", resolved)}
		}
		return pathResolvedMsg{side: which, path: resolved}
	}
}

func joinSelectedSummary(entries []filesystem.Entry) string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
		if len(names) == 3 {
			break
		}
	}
	if len(entries) > len(names) {
		names = append(names, fmt.Sprintf("+%d more", len(entries)-len(names)))
	}
	return strings.Join(names, ", ")
}
