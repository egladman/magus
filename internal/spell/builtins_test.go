package spell

import (
	"testing"

	"github.com/egladman/magus/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltins_NonEmpty(t *testing.T) {
	m := Builtins()
	require.NotEmpty(t, m, "Builtins() returned empty map")
	for key, s := range m {
		assert.NotEmptyf(t, s.Name, "Builtins()[%q].Name is empty", key)
		// The registry is keyed by runtime name, so the key is the spell's Name.
		assert.Equalf(t, key, s.Name, "Builtins() key %q != Descriptor.Name %q", key, s.Name)
	}
}

func TestBuiltins_KeyedByName(t *testing.T) {
	m := Builtins()
	// The golang spell renames itself to "go": it must be reachable by name…
	assert.Contains(t, m, "go", `Builtins()["go"] not found`)
	// …and not by its source directory.
	assert.NotContains(t, m, "golang", `Builtins()["golang"] present — registry is keyed by name, not source dir`)
}

func TestBuiltinsHash_Format(t *testing.T) {
	h := BuiltinsHash()
	assert.Len(t, h, 64, "BuiltinsHash() should be 64 chars (SHA-256 hex)")
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			assert.Failf(t, "non-hex character", "BuiltinsHash() contains non-hex character %q", c)
			break
		}
	}
}

func TestBuiltinsHash_Stable(t *testing.T) {
	h1, h2 := BuiltinsHash(), BuiltinsHash()
	assert.Equal(t, h1, h2, "BuiltinsHash() not stable")
}

func TestGoSpell_TidyTarget(t *testing.T) {
	goSpell := Builtins()["go"]
	tidy, ok := goSpell.Ops["go-mod-tidy"]
	require.Truef(t, ok, "go spell has no go-mod-tidy target; targets: %v", goSpell.OpNames())
	// Default (no write charm): check mode via --diff (non-zero exit if changes
	// are needed — safe for CI gating).
	assert.Equal(t, "go", tidy.Cmd)
	assert.Equal(t, []string{"mod", "tidy", "--diff"}, tidy.Args)
	// rw charm drops --diff (remove /2) so tidy actually applies the changes.
	w, ok := tidy.Charms["rw"]
	require.True(t, ok, "tidy has no rw charm")
	assert.Equal(t, []types.PatchOp{{Op: "remove", Path: "/2"}}, w.Ops)
}
