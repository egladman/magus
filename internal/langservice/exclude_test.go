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
	available := []string{
		"platform", "crypto", "env", "json", "time", "fmt", "markdown", "charm",
		"encoding", "path", "strings", "semver", "yaml", "template", "toml", "uuid",
		"magus",
	}
	got := names(ExcludedModules(available))
	assert.ElementsMatch(t, []string{"os", "fs", "vcs", "archive", "http"}, got,
		"only the process/filesystem/network modules should be excluded")

	// magus is available here (it is in the set), so it must never be reported as
	// excluded - the point of sourcing the set from real registration.
	assert.NotContains(t, got, "magus")
}
