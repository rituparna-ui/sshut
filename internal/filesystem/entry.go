package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// KindFromMode classifies a file mode without dereferencing symbolic links.
func KindFromMode(mode os.FileMode) Kind {
	switch {
	case mode.IsDir():
		return KindDirectory
	case mode&os.ModeSymlink != 0:
		return KindSymlink
	case mode.IsRegular():
		return KindFile
	default:
		return KindOther
	}
}

// EntryFromInfo converts os.FileInfo metadata into the common entry type.
func EntryFromInfo(path string, info os.FileInfo) Entry {
	return Entry{
		Name:    info.Name(),
		Path:    path,
		Kind:    KindFromMode(info.Mode()),
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime(),
	}
}

// Sort orders directories first, then names case-insensitively with a
// case-sensitive tie breaker for deterministic display.
func Sort(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		if left.IsDir() != right.IsDir() {
			return left.IsDir()
		}
		leftName, rightName := strings.ToLower(left.Name), strings.ToLower(right.Name)
		if leftName != rightName {
			return leftName < rightName
		}
		return left.Name < right.Name
	})
}

// ValidateName checks that a user-supplied entry name cannot change its
// parent directory. Backslashes are rejected as well so names are safe when
// the same workflow is used across local platforms.
func ValidateName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("name cannot be empty")
	case name == "." || name == "..":
		return fmt.Errorf("%q is not a valid name", name)
	case strings.ContainsRune(name, '\x00'):
		return fmt.Errorf("name cannot contain a null byte")
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("name cannot contain a path separator")
	case filepath.Base(name) != name:
		return fmt.Errorf("%q is not a valid name", name)
	default:
		return nil
	}
}
