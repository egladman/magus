package main

import (
	"fmt"
)

// version, commit, and buildDate are injected by the linker at build time:
//
//	go build -ldflags "-X main.version=v0.1.0 -X main.commit=abc123 -X main.buildDate=2026-05-06"
//
// The default "unknown" is the dev-build sentinel the proc adoption gate keys on to
// fingerprint an unstamped build (proc.devVersionSentinel); keep the two in sync.
var (
	version   = "unknown"
	commit    = "unknown"
	buildDate = "unknown"
)

func runVersion(args []string) {
	fmt.Printf("magus %s (%s) built %s\n", version, commit, buildDate)
	if hasVerboseFlag(args) {
		fmt.Printf("engine: buzz\n")
	}
}

// hasVerboseFlag reports whether args contains -v or --verbose.
func hasVerboseFlag(args []string) bool {
	for _, a := range args {
		if a == "-v" || a == "--verbose" {
			return true
		}
	}
	return false
}
