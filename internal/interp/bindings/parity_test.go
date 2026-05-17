package bindings_test

import (
	"slices"
	"testing"

	"github.com/egladman/magus/project"
)

// TestEngineSpecParity locks the engine-agnostic mgs_ contract: a spell
// declaring every optional mgs_ function with record-shaped ops must resolve to
// the same Spec whether it is authored in Buzz or in Teal. It guards against the
// two resolvers (internal/spell/resolve.go and resolveLua in spell.go) drifting —
// the bug that previously dropped mgs_listClaimedGlobs on the Teal/Lua path.
// claims is asserted explicitly because that is the field that regressed.
// Function-valued op handlers are deliberately Buzz-only (see contract.go) and so
// are out of this test's scope.
func TestEngineSpecParity(t *testing.T) {
	buzzSrc := `export fun mgs_getName() > str { return "parity_buzz"; }
export fun mgs_listRequiredGlobs(_dir: str) > [str] { return ["**/*.rb", "Gemfile.lock"]; }
export fun mgs_listProvidedGlobs() > [str] { return ["vendor/bundle/**"]; }
export fun mgs_listClaimedGlobs() > [str] { return [".rubocop.yml", "Gemfile"]; }
export fun mgs_getVersionCommand() > [str] { return ["ruby", "--version"]; }
export fun mgs_isForeignProcess() > bool { return false; }
export fun mgs_listTargets() > any {
    return {"rspec": {"cmd": "bundle", "args": ["exec", "rspec"]}};
}
`
	tealSrc := `
return {
    mgs_getName = function(): string return "parity_teal" end,
    mgs_listRequiredGlobs = function(_dir: string): {string} return {"**/*.rb", "Gemfile.lock"} end,
    mgs_listProvidedGlobs = function(): {string} return {"vendor/bundle/**"} end,
    mgs_listClaimedGlobs = function(): {string} return {".rubocop.yml", "Gemfile"} end,
    mgs_getVersionCommand = function(): {string} return {"ruby", "--version"} end,
    mgs_isForeignProcess = function(): boolean return false end,
    mgs_listTargets = function(): any
        return { rspec = { cmd = "bundle", args = {"exec", "rspec"} } }
    end,
}
`

	loadAndBind := func(t *testing.T, ext, src string) {
		t.Helper()
		dir := t.TempDir()
		t.Chdir(dir)
		name := "spells/parity." + ext
		writeFile(t, dir, name, src)
		writeFile(t, dir, "magusfile.bzz", `import "magus";
final sp = magus.spell.load("`+name+`");
magus.project.register(".", {"spells": [sp]});`)
		if err := parseMagusfile(t, dir); err != nil {
			t.Fatalf("parse (%s): %v", ext, err)
		}
	}

	loadAndBind(t, "bzz", buzzSrc)
	loadAndBind(t, "tl", tealSrc)

	buzzSp, ok := project.DefaultSpellRegistry().Lookup("parity_buzz")
	if !ok {
		t.Fatal("parity_buzz not registered")
	}
	tealSp, ok := project.DefaultSpellRegistry().Lookup("parity_teal")
	if !ok {
		t.Fatal("parity_teal not registered")
	}

	eq := func(field string, a, b []string) {
		t.Helper()
		if !slices.Equal(a, b) {
			t.Errorf("%s differ across engines: buzz=%v teal=%v", field, a, b)
		}
	}
	eq("sources (needs)", buzzSp.Sources(), tealSp.Sources())
	eq("outputs (provides)", buzzSp.Outputs(), tealSp.Outputs())
	eq("targets (ops)", buzzSp.Targets(), tealSp.Targets())
	// claims is the field the Lua resolver previously dropped — assert it directly.
	eq("claims", buzzSp.Claims(), tealSp.Claims())
	if want := []string{".rubocop.yml", "Gemfile"}; !slices.Equal(buzzSp.Claims(), want) {
		t.Errorf("claims = %v, want %v (both engines must carry mgs_listClaimedGlobs)", buzzSp.Claims(), want)
	}
	if buzzSp.ForeignProcess() != tealSp.ForeignProcess() {
		t.Errorf("foreignProcess differ: buzz=%v teal=%v", buzzSp.ForeignProcess(), tealSp.ForeignProcess())
	}
	// Op parity is asserted by name here; a record op's cmd/args are part of the
	// resolved Spec and are covered by the extract tests (extract_test.go,
	// resolve_test.go). RenderCommand is deliberately not cross-checked: the
	// workspace .bzz load path (loadBuzzSpell) wires no static command renderer,
	// unlike the Teal path — a registration difference, separate from the mgs_
	// resolver parity this test guards.
}
