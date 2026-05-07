package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/std"
)

// TestModuleDocsUpToDate verifies the checked-in docs/modules/*.md are exactly
// what magus-docs-gen would emit today, and that the committed set matches the
// registered module set — no orphan (deleted module) or missing (new module)
// docs. This is the gate the docs lacked when they drifted (sh.md outlived the
// sh module; crypto/platform were never generated).
func TestModuleDocsUpToDate(t *testing.T) {
	docsDir := filepath.Join("..", "..", "docs", "modules")

	modules := std.All()
	slices.SortFunc(modules, func(a, b std.Module) int { return strings.Compare(a.Name, b.Name) })

	expected := map[string]bool{"index.md": true}
	for _, m := range modules {
		expected[m.Name+".md"] = true
		got, err := os.ReadFile(filepath.Join(docsDir, m.Name+".md"))
		if err != nil {
			t.Errorf("read %s.md: %v", m.Name, err)
			continue
		}
		if string(got) != renderModule(m) {
			t.Errorf("%s.md is out of date; re-run:\n  go run ./cmd/magus-docs-gen -out ./docs/modules", m.Name)
		}
	}

	if got, err := os.ReadFile(filepath.Join(docsDir, "index.md")); err != nil {
		t.Errorf("read index.md: %v", err)
	} else if string(got) != renderIndex(modules) {
		t.Errorf("index.md is out of date; re-run:\n  go run ./cmd/magus-docs-gen -out ./docs/modules")
	}

	committed, err := filepath.Glob(filepath.Join(docsDir, "*.md"))
	if err != nil {
		t.Fatalf("glob docs: %v", err)
	}
	for _, p := range committed {
		if base := filepath.Base(p); !expected[base] {
			t.Errorf("orphaned doc %s: no module registers it; delete it (re-run magus-docs-gen)", base)
		}
	}
}
