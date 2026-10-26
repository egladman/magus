package bindings

import (
	"os"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/spells"
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
// tell a dropped hook from an undeclared one: a declared version probe simply never
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
	// applies each: what a construction path either calls or forgets.
	// Matched WITH the open paren. Without it "WithVersionProbe" is a substring of
	// "WithVersionProbeNamed", so the unnamed probe could never fail independently;
	// a mutation test caught this test lying about its own coverage.
	wantOptions := []string{
		"WithTools(",
		"WithVersionProber(",
		"WithLanguage(",
		"WithOpaque(",
	}

	// localSpellBaseOptions is the one list every workspace-local construction path
	// builds from, so it is the file that must name each hook, and a path that builds
	// elsewhere is itself the regression.
	data, err := os.ReadFile("spell.go")
	require.NoError(t, err)
	for _, opt := range wantOptions {
		if !strings.Contains(string(data), opt) {
			t.Errorf("spell.go never applies types.%s\n"+
				"A descriptor carrying that hook would have it silently dropped here: the spell "+
				"registers, the hook decodes, and nothing downstream can tell it was lost.", opt)
		}
	}
	data, err = os.ReadFile("spell_buzz.go")
	require.NoError(t, err)
	assert.Contains(t, string(data), "localSpellBaseOptions(", "spell_buzz.go builds its spell from the shared option list")
}

// A workspace-local spell's handle carries what a magusfile can use and no more: the
// descriptor stays behind in the registry, where the import registered the spell.
func TestSpellHandleCarriesNameGlobsAndOps(t *testing.T) {
	h := spellHandleFromMeta(spells.Descriptor{
		Name:     "myspell",
		Needs:    []string{"**/*.go"},
		Provides: []string{"gen/**"},
		Ops:      map[string]spells.Op{"test": {Command: spells.Command{Bin: "go", Args: []string{"test"}}}},
	})
	assert.ElementsMatch(t, []string{"name", "needs", "provides", "listTargets", "test"}, h.MapKeys())
	name, _ := h.MapGet("name")
	assert.Equal(t, "myspell", name.AsString())
}

// execContextValue builds what ctx.withEnv/withCwd produce: a marked map carrying only
// execution overrides.
func execContextValue(env map[string]string, cwd string) vm.Value {
	m := vm.NewMap()
	m.MapSet(ctxMarker, vm.BoolValue(true))
	if env != nil {
		e := vm.NewMap()
		for k, v := range env {
			e.MapSet(k, vm.StringValue(v))
		}
		m.MapSet("env", e)
	}
	if cwd != "" {
		m.MapSet("cwd", vm.StringValue(cwd))
	}
	return m
}

// TestCtxOverridesReadFromAMarkedContext pins that a leading magus\Exec is recognized
// and its overrides extracted. This is asserted at the binding rather than through a
// subprocess deliberately: an earlier round of this was "verified" by shelling out to
// an op that did not exist, so the assertion passed because nothing ran. A unit test on
// the extractor cannot pass for that reason.
func TestCtxOverridesReadFromAMarkedContext(t *testing.T) {
	args := []vm.Value{execContextValue(map[string]string{"CGO_ENABLED": "0"}, "sub")}
	got, consumed := contextOverridesFromBuzz(args, 0)

	require.Equal(t, 1, consumed, "a marked context must be consumed as argument one")
	assert.Equal(t, commandOpts{env: map[string]string{"CGO_ENABLED": "0"}, cwd: "sub"}, got, "env and cwd overrides, and nothing else")
}

// TestPlainOptsTableIsNotAContext is the other half: an ordinary {args: [...]} table
// must NOT be mistaken for a context, or the required-ctx check would accept the old
// call form and silently read opts as overrides.
func TestPlainOptsTableIsNotAContext(t *testing.T) {
	o := vm.NewMap()
	o.MapSet("args", vm.StringValue("-race"))
	o.MapSet("env", vm.NewMap())

	_, consumed := contextOverridesFromBuzz([]vm.Value{o}, 0)
	assert.Zero(t, consumed, "an unmarked map is an opts table, never a context")
}
