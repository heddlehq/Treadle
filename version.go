// Package treadle holds the version of the app.
package treadle

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var version string

// Version comes from the VERSION file, the only place it's written down. Embedding
// it means every build knows its version, however it was built.
var Version = strings.TrimSpace(version)
