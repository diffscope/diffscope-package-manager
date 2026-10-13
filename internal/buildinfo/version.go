package buildinfo

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var baseVersion string

// Suffix contains the executable version suffix injected by the linker.
var Suffix = "+dev"

// Version returns the executable version.
func Version() string {
	return strings.TrimSpace(baseVersion) + Suffix
}
