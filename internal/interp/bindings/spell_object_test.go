package bindings

import (
	"os"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// execCtxValue builds what ctx.withEnv/withCwd produce: a marked map carrying only
// execution overrides.
func execCtxValue(env map[string]string, cwd string) vm.Value {
	m := vm.NewMap()
	m.MapSet(ctxMarker, vm.BoolValue(true))
	if env != nil {
		e := vm.NewMap()
		for k, v := range env {
			e.MapSet(k, vm.StrValue(v))
		}
		m.MapSet("env", e)
	}
	if cwd != "" {
		m.MapSet("cwd", vm.StrValue(cwd))
	}
	return m
}

// TestCtxOverridesReadFromAMarkedContext pins that a leading magus\Exec is recognized
// and its overrides extracted. This is asserted at the binding rather than through a
// subprocess deliberately: an earlier round of this was "verified" by shelling out to
// an op that did not exist, so the assertion passed because nothing ran. A unit test on
// the extractor cannot pass for that reason.
func TestCtxOverridesReadFromAMarkedContext(t *testing.T) {
	args := []vm.Value{execCtxValue(map[string]string{"CGO_ENABLED": "0"}, "sub")}
	got, consumed := ctxOverridesFromBuzz(args, 0)

	require.Equal(t, 1, consumed, "a marked context must be consumed as argument one")
	assert.Equal(t, map[string]string{"CGO_ENABLED": "0"}, got.env, "env override")
	assert.Equal(t, "sub", got.cwd, "cwd override")
}

// TestPlainOptsTableIsNotAContext is the other half: an ordinary {args: [...]} table
// must NOT be mistaken for a context, or the required-ctx check would accept the old
// call form and silently read opts as overrides.
func TestPlainOptsTableIsNotAContext(t *testing.T) {
	o := vm.NewMap()
	o.MapSet("args", vm.StrValue("-race"))
	o.MapSet("env", vm.NewMap())

	_, consumed := ctxOverridesFromBuzz([]vm.Value{o}, 0)
	assert.Zero(t, consumed, "an unmarked map is an opts table, never a context")
}
