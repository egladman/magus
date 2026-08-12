package langservice

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// names flattens modules to their names for set assertions.
func names(mods []Module) []string {
	out := make([]string, len(mods))
	for i, m := range mods {
		out[i] = m.Name
	}
	return out
}

func TestExcludedModules(t *testing.T) {
	// The playground registers the pure-compute modules plus magus (wired as a
	// global). Everything else in the manifest is reference-only there.
	//
	// This list mirrors the WASM-capable entries of
	// internal/interp/bindings/gen/modules_wasm.go, which is what the playground
	// actually installs. Keep the two together when a module is added: the list
	// went stale once already, when the stdlib expansion added thirteen modules
	// and nothing here noticed, so every one of them read as "excluded" - which
	// would have told a playground user that base64 and math were unavailable.
	available := []string{
		"platform", "crypto", "env", "json", "time", "fmt", "markdown", "charm",
		"path", "strings", "semver", "yaml", "template", "toml", "uuid", "xml",
		"base64", "csv", "diff", "hex", "ini", "log", "math", "sort", "url",
		"magus",
	}
	got := names(ExcludedModules(available))
	assert.ElementsMatch(t, []string{
		"os", "fs", "vcs", "archive", "http", // process / filesystem / network
		"net", "proc", "term", // same three categories, added later
		"lcov", // reads a coverage file from disk
	}, got, "only the process/filesystem/network modules should be excluded")

	// magus is available here (it is in the set), so it must never be reported as
	// excluded - the point of sourcing the set from real registration.
	assert.NotContains(t, got, "magus")
}
