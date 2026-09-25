package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/rituu/sshut/internal/app"
)

func main() {
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: sshut [user@host]")
		os.Exit(2)
	}
	destination := ""
	if len(os.Args) == 2 {
		destination = os.Args[1]
	}

	finalModel, err := tea.NewProgram(app.New(destination)).Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshut: %v\n", err)
		os.Exit(1)
	}
	if closer, ok := finalModel.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "sshut: %v\n", err)
			os.Exit(1)
		}
	}
}
