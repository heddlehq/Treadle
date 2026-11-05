package treadle

import (
	"regexp"
	"testing"
)

func TestVersionIsSemantic(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(Version) {
		t.Fatalf("VERSION is %q, want MAJOR.MINOR.PATCH", Version)
	}
}
