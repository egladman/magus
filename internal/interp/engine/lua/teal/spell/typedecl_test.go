package spell

import (
	"strings"
	"testing"
)

// TestSpellTypeDeclSurface guards the spell typed surface: ops are invoked
// through per-target method fields and listTargets() introspects them, but there is
// no runTarget — dispatch-by-name was dropped, so a .tl calling it must not type-check.
func TestSpellTypeDeclSurface(t *testing.T) {
	decl := SpellTypeDecl()
	if !strings.Contains(decl, "listTargets:") {
		t.Error("spell records should still expose listTargets() for introspection")
	}
	if strings.Contains(decl, "runTarget") {
		t.Error("spell records should no longer expose runTarget")
	}
	// Per-target op methods must still be emitted. Op names follow the CLI command,
	// so kebab ops are bracket fields (the go spell's golangci-lint op).
	if !strings.Contains(decl, `["golangci-lint"]:`) {
		t.Error(`MagusSpell should expose per-op method fields (e.g. ["golangci-lint"])`)
	}
	// Workspace spells type any op via the __index fallback.
	if !strings.Contains(decl, "global record WorkspaceSpell") || !strings.Contains(decl, "metamethod __index") {
		t.Error("WorkspaceSpell with __index fallback should be emitted for local spells")
	}
}
