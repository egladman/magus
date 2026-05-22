package bindings_test

import (
	"slices"
	"testing"

	"github.com/egladman/magus/project"
)

// TestEngineSpecParity locks the engine-agnostic mgs_ contract: a Buzz spell
// declaring every optional mgs_ function with record-shaped ops resolves to the
// expected Spec. It guards the resolver (internal/spell/resolve.go) against
// dropping fields — claims is asserted explicitly because that is the field that
// previously regressed.
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

	loadAndBind := func(t *testing.T, ext, src string) {
		t.Helper()
		dir := t.TempDir()
		t.Chdir(dir)
		name := "spells/parity." + ext
		writeFile(t, dir, name, src)
		writeFile(t, dir, "magusfile.bzz", `import "magus";
const sp = magus.spell.load("`+name+`");
magus.project.register(".", {"spells": [sp]});`)
		if err := parseMagusfile(t, dir); err != nil {
			t.Fatalf("parse (%s): %v", ext, err)
		}
	}

	loadAndBind(t, "bzz", buzzSrc)

	buzzSp, ok := project.DefaultSpellRegistry().Lookup("parity_buzz")
	if !ok {
		t.Fatal("parity_buzz not registered")
	}

	if want := []string{"**/*.rb", "Gemfile.lock"}; !slices.Equal(buzzSp.Sources(), want) {
		t.Errorf("sources = %v, want %v", buzzSp.Sources(), want)
	}
	if want := []string{"vendor/bundle/**"}; !slices.Equal(buzzSp.Outputs(), want) {
		t.Errorf("outputs = %v, want %v", buzzSp.Outputs(), want)
	}
	if want := []string{"rspec"}; !slices.Equal(buzzSp.Targets(), want) {
		t.Errorf("targets = %v, want %v", buzzSp.Targets(), want)
	}
	// claims is the field the resolver previously dropped — assert it directly.
	if want := []string{".rubocop.yml", "Gemfile"}; !slices.Equal(buzzSp.Claims(), want) {
		t.Errorf("claims = %v, want %v (mgs_listClaimedGlobs must be carried)", buzzSp.Claims(), want)
	}
	if buzzSp.ForeignProcess() {
		t.Errorf("foreignProcess = true, want false")
	}
}
