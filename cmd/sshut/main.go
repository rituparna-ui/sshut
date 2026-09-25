package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 {
		fmt.Fprintf(os.Stderr, "sshut: connecting is not implemented yet\n")
		os.Exit(1)
	}

	fmt.Println("sshut: terminal file manager")
}
