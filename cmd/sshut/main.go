package main

import (
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/rituu/sshut/internal/app"
)

func main() {
	args := os.Args[1:]
	if len(args) > 1 {
		usage(os.Stderr)
		os.Exit(2)
	}
	if len(args) == 1 {
		switch args[0] {
		case "-h", "--help":
			usage(os.Stdout)
			return
		default:
			if len(args[0]) > 0 && args[0][0] == '-' {
				usage(os.Stderr)
				os.Exit(2)
			}
		}
	}

	destination := ""
	if len(args) == 1 {
		destination = args[0]
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

func usage(output io.Writer) {
	fmt.Fprintln(output, "usage: sshut [user@host]")
	fmt.Fprintln(output, "       sshut --help")
}
