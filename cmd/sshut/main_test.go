package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestUsage(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	usage(&output)
	text := output.String()
	for _, want := range []string{"usage: sshut", "--help"} {
		if !strings.Contains(text, want) {
			t.Fatalf("usage output %q does not contain %q", text, want)
		}
	}
}
