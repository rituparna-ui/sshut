package filesystem

import (
	"os"
	"testing"
)

func TestKindFromMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode os.FileMode
		want Kind
	}{
		{name: "file", mode: 0o644, want: KindFile},
		{name: "directory", mode: os.ModeDir | 0o755, want: KindDirectory},
		{name: "symlink", mode: os.ModeSymlink | 0o777, want: KindSymlink},
		{name: "device", mode: os.ModeDevice, want: KindOther},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := KindFromMode(test.mode); got != test.want {
				t.Fatalf("KindFromMode(%v) = %v, want %v", test.mode, got, test.want)
			}
		})
	}
}

func TestSortDirectoriesFirst(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Name: "zebra.txt"},
		{Name: "Beta.txt"},
		{Name: "src", Kind: KindDirectory},
		{Name: "alpha.txt"},
		{Name: "docs", Kind: KindDirectory},
	}
	Sort(entries)

	want := []string{"docs", "src", "alpha.txt", "Beta.txt", "zebra.txt"}
	for i, name := range want {
		if entries[i].Name != name {
			t.Fatalf("entry %d = %q, want %q", i, entries[i].Name, name)
		}
	}
}

func TestValidateName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"folder", "file.txt", "name with spaces"} {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) returned %v", name, err)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "bad\x00name"} {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) unexpectedly succeeded", name)
		}
	}
}
