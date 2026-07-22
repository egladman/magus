package main

import (
	"strings"
	"testing"
)

// TestReadExampleHonorsSpellsDir guards the regression where per-op examples were read
// relative to the process cwd: run from this package directory (not the repo root), a
// correct -spells path must still locate the example. Without the fix `magus run
// generate` (which runs the tool from docs/) silently dropped every Example section.
func TestReadExampleHonorsSpellsDir(t *testing.T) {
	orig := spellsDir
	t.Cleanup(func() { spellsDir = orig })
	spellsDir = "../../spells" // spells/ relative to cmd/magus-spelldocs/

	ex := readExample("go", "go-build")
	if ex == "" {
		t.Fatal("readExample found no go/go-build example with -spells set; the path is not honored")
	}
	if !strings.Contains(ex, "go-build") {
		t.Errorf("example does not reference the op it documents: %q", ex)
	}
}
