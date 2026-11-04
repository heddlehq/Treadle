package main

import (
	"log"

	"treadle/internal/tui"
)

func main() {
	p := tui.NewProgram()

	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
