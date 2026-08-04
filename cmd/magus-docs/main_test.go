package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/std"
)

// TestModuleDocsUpToDate verifies the checked-in docs/modules/*.md are exactly
// what magus-docs would emit today, and that the committed set matches the
// registered module set: no orphan (deleted module) or missing (new module) docs.
// This is the gate the docs lacked when they drifted (sh.md outlived the sh
// module; crypto/platform were never generated).
func TestModuleDocsUpToDate(t *testing.T) {
	docsDir := filepath.Join("..", "..", "docs", "reference", "buzz")

	modules := std.All()
	slices.SortFunc(modules, func(a, b std.Module) int { return strings.Compare(a.Name, b.Name) })

	expected := map[string]bool{"index.md": true}
	for _, m := range modules {
		expected[m.Name+".md"] = true
		got, err := os.ReadFile(filepath.Join(docsDir, m.Name+".md"))
		if !assert.NoError(t, err, "read %s.md", m.Name) {
			continue
		}
		assert.Equal(t, renderModule(m), string(got),
			"%s.md is out of date; re-run:\n  go run ./cmd/magus-docs -out ./docs/reference/buzz", m.Name)
	}

	if got, err := os.ReadFile(filepath.Join(docsDir, "index.md")); assert.NoError(t, err, "read index.md") {
		assert.Equal(t, renderIndex(modules), string(got),
			"index.md is out of date; re-run:\n  go run ./cmd/magus-docs -out ./docs/reference/buzz")
	}

	committed, err := filepath.Glob(filepath.Join(docsDir, "*.md"))
	require.NoError(t, err, "glob docs")
	for _, p := range committed {
		base := filepath.Base(p)
		assert.True(t, expected[base],
			"orphaned doc %s: no module registers it; delete it (re-run magus-docs)", base)
	}
}

// TestRenderModuleSeeAlso checks the "## See also" section every module page
// ends with: a link back to the index, plus the module's curated siblings (or
// just the index link when it has none). This is the fix for the link-audit
// finding that every stdlib module page was a dead end with zero outbound
// links.
func TestRenderModuleSeeAlso(t *testing.T) {
	byName := make(map[string]std.Module)
	for _, m := range std.All() {
		byName[m.Name] = m
	}

	fs, ok := byName["fs"]
	require.True(t, ok, "fs module must be registered")
	out := renderModule(fs)
	assert.Contains(t, out, "## See also\n\n")
	assert.Contains(t, out, "- [Standard library modules](index.md)\n")
	assert.Contains(t, out, "- [`path`](path.md)\n")
	assert.Contains(t, out, "- [`os`](os.md)\n")
	assert.Contains(t, out, "- [`archive`](archive.md)\n")

	// A module with no curated sibling (e.g. semver) still gets the index
	// link and nothing else.
	semver, ok := byName["semver"]
	require.True(t, ok, "semver module must be registered")
	out = renderModule(semver)
	assert.True(t, strings.HasSuffix(out, "## See also\n\n- [Standard library modules](index.md)\n"),
		"semver has no curated siblings; See also must end with just the index link, got:\n%s", out)
}

// TestModuleSiblingsReferenceRealModules guards against a typo in
// moduleSiblings silently linking to a module page that does not exist.
func TestModuleSiblingsReferenceRealModules(t *testing.T) {
	byName := make(map[string]bool)
	for _, m := range std.All() {
		byName[m.Name] = true
	}
	for name, sibs := range moduleSiblings {
		assert.True(t, byName[name], "moduleSiblings key %q is not a registered module", name)
		for _, s := range sibs {
			assert.True(t, byName[s], "moduleSiblings[%q] references unregistered module %q", name, s)
		}
	}
}
