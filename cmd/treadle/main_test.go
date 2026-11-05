package main

import (
	"bytes"
	"strings"
	"testing"

	"treadle"
)

func TestVersionFlagsPrintTheVersion(t *testing.T) {
	for _, flag := range []string{"--version", "-v", "version"} {
		var stdout, stderr bytes.Buffer

		handled, code := runCommand([]string{flag}, &stdout, &stderr)

		if !handled || code != 0 {
			t.Fatalf("%s: handled = %v, code = %d", flag, handled, code)
		}
		if got, want := stdout.String(), "treadle "+treadle.Version+"\n"; got != want {
			t.Fatalf("%s: printed %q, want %q", flag, got, want)
		}
	}
}

func TestNoArgumentsStartTheEditor(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if handled, _ := runCommand(nil, &stdout, &stderr); handled {
		t.Fatal("no arguments should start the editor")
	}
}

func TestUnknownOptionsFailWithUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer

	handled, code := runCommand([]string{"--nope"}, &stdout, &stderr)

	if !handled || code != 2 {
		t.Fatalf("handled = %v, code = %d", handled, code)
	}
	if !strings.Contains(stderr.String(), "--nope") || !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
