package main

import (
	"fmt"
	"io"
	"log"
	"os"

	"treadle"
	"treadle/internal/tui"
)

const usage = `Treadle is a terminal workflow editor for Heddle.

Usage:
  treadle             start the editor
  treadle --version   print the version
  treadle --help      print this help
`

func main() {
	if handled, code := runCommand(os.Args[1:], os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}

	p := tui.NewProgram()

	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

// runCommand answers anything that doesn't start the editor.
func runCommand(args []string, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}

	switch args[0] {
	case "--version", "-v", "version":
		fmt.Fprintf(stdout, "treadle %s\n", treadle.Version)
		return true, 0
	case "--help", "-h", "help":
		fmt.Fprint(stdout, usage)
		return true, 0
	default:
		fmt.Fprintf(stderr, "Unknown option: %s\n\n%s", args[0], usage)
		return true, 2
	}
}
