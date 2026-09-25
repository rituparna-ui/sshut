package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/rituu/sshut/internal/app"
)

func main() {
	if len(os.Args) > 1 {
		fmt.Fprintln(os.Stderr, "usage: sshut")
		os.Exit(2)
	}

	if _, err := tea.NewProgram(app.NewLocal()).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "sshut: %v\n", err)
		os.Exit(1)
	}
}
