package bindings

import (
	"os"
	"strings"
	"testing"
)

// TestSpellOptionsApplied guards declaration-versus-application drift: every file that
// builds a *spells.Spell from a Descriptor must wire every optional mgs_ hook the
// descriptor can carry.
//
// This is the bug it exists to prevent, which shipped and stayed invisible. Workspace-
// local Buzz spells lost their version probe, language, and opaque flag because
// spell_buzz.go built the spell without those options while spell.go passed all of
// them. The descriptor decoded correctly the whole time, so nothing downstream could
// tell a dropped hook from an undeclared one - a declared version probe simply never
// ran and never entered the cache key, and a local spell's toolchain could drift with
// nothing invalidating.
//
// A doctor check cannot catch this. Doctor only ever observes the REGISTERED spell,
// where a dropped hook is indistinguishable from one that was never declared. The
// asymmetry exists only between magus's own construction paths, which is why this is
// a test over those files rather than a workspace diagnostic.
//
// Deliberately file-level rather than function-level: it is coarse, and coarse is the
// point. It answers "does this file know about this option at all", which is exactly
// the question that was answered wrongly, and it does not break when a constructor is
// split or renamed.
func TestSpellOptionsApplied(t *testing.T) {
	// The optional hooks that carry behaviour onto a spell, keyed by the option that
	// applies each - what a construction path either calls or forgets.
	// Matched WITH the open paren. Without it "WithVersionProbe" is a substring of
	// "WithVersionProbeNamed", so the unnamed probe could never fail independently -
	// a mutation test caught this test lying about its own coverage.
	wantOptions := []string{
		"WithTools(",
		"WithVersionProber(",
		"WithLanguage(",
		"WithOpaque(",
	}

	// Every file that constructs a spell from a descriptor. A new construction path
	// added outside this list is itself the regression, so keep it current.
	buildFiles := []string{
		"spell.go",      // built-in registration and the magusfile-declared path
		"spell_buzz.go", // workspace-local spells/<name>/spell.buzz
	}

	for _, file := range buildFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		src := string(data)
		for _, opt := range wantOptions {
			if !strings.Contains(src, opt) {
				t.Errorf("%s never applies types.%s\n"+
					"A descriptor carrying that hook would have it silently dropped here: the spell "+
					"registers, the hook decodes, and nothing downstream can tell it was lost.", file, opt)
			}
		}
	}
}
