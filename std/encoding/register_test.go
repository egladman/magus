package encoding

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModulesMatchDirectories guards leafSets against drift from the actual
// std/encoding/* directories, the same shape of guard TestModulesMatchStd
// (internal/interp/bindings/gen) already runs for the hand-maintained
// Modules registry there: leafSets is hand-maintained too (see its doc), and
// this is what turns "added a leaf package, forgot the line in leafSets" (or
// the reverse - a stale entry for a directory that no longer exists) into a
// failing test instead of a module that compiles clean and never binds.
func TestModulesMatchDirectories(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	wantDirs := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(e.Name() + "/register.go"); err != nil {
			continue // not a leaf module directory
		}
		wantDirs[e.Name()] = true
	}
	require.NotEmpty(t, wantDirs, "no std/encoding/*/register.go directories found; the test's own directory walk is broken")

	gotNames := map[string]bool{}
	for _, m := range Modules() {
		gotNames[m.Name] = true
		assert.Equalf(t, "encoding/"+m.Name, m.Path,
			"module %q's Path must be encoding/%[1]s to match its directory", m.Name)
	}

	assert.Equal(t, wantDirs, gotNames,
		"leafSets in register.go must name exactly the std/encoding/*/register.go directories")
}
