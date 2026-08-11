package std

import _ "embed"

// The Buzz-implemented half of the standard library. Each source is embedded and
// registered exactly like a Go module; see source_module.go for why a stdlib
// module would be written in Buzz at all, and for the performance trade that says
// when it should not be.

//go:embed lib/lcov.buzz
var lcovSource string

func init() {
	RegisterSource(SourceModule{
		Name:   "lcov",
		Doc:    "LCOV coverage reports: the percentage a badge or a floor gate shows, and the line-level merge that keeps it true across multiple test processes.",
		Source: lcovSource,
	})
}
