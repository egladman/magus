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
	docsDir := filepath.Join("..", "..", "docs", "buzz", "modules")

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
			"%s.md is out of date; re-run:\n  go run ./cmd/magus-docs -out ./docs/buzz/modules", m.Name)
	}

	if got, err := os.ReadFile(filepath.Join(docsDir, "index.md")); assert.NoError(t, err, "read index.md") {
		assert.Equal(t, renderIndex(modules), string(got),
			"index.md is out of date; re-run:\n  go run ./cmd/magus-docs -out ./docs/buzz/modules")
	}

	committed, err := filepath.Glob(filepath.Join(docsDir, "*.md"))
	require.NoError(t, err, "glob docs")
	for _, p := range committed {
		base := filepath.Base(p)
		assert.True(t, expected[base],
			"orphaned doc %s: no module registers it; delete it (re-run magus-docs)", base)
	}
}
