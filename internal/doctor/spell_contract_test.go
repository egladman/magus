package doctor

import (
	"strings"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckSpellContract(t *testing.T) {
	t.Run("no spells", func(t *testing.T) {
		got := checkSpellContract(nil)
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, "no spells registered", got.Message)
	})

	// The one thing no spell can function without, and the only required half of the
	// contract.
	t.Run("unnamed spell", func(t *testing.T) {
		got := checkSpellContract([]*spells.Spell{spells.NewSpell("")})
		assert.Equal(t, types.DoctorFail, got.Status)
		assert.Contains(t, got.Message, "do not satisfy the required mgs_ contract")
		require.Len(t, got.Details, 1)
		assert.Contains(t, got.Details[0], "<unnamed>: mgs_getName returned an empty name")
	})

	// An empty target list is NOT a defect: the magusfile spell contributes the targets
	// a magusfile exports, so it declares none statically and is entirely correct.
	t.Run("targets supplied by the magusfile", func(t *testing.T) {
		got := checkSpellContract([]*spells.Spell{spells.NewSpell("magusfile")})
		assert.Equal(t, types.DoctorOK, got.Status)
		require.Len(t, got.Details, 1)
		assert.Contains(t, got.Details[0], "targets only (no optional hooks)")
		assert.Contains(t, got.Details[0], "targets supplied by the magusfile, not the spell")
	})

	t.Run("every optional hook", func(t *testing.T) {
		full := spells.NewSpell("go",
			spells.WithTargets("build"),
			spells.WithSources("**/*.go"),
			spells.WithOutputs("bin/**"),
			spells.WithIgnoreDirs("vendor"),
			spells.WithTools(map[string]spells.Tool{
				"go": {Probe: spells.Command{Bin: "go", Args: []string{"version"}}},
			}),
			spells.WithLanguage("go"),
			spells.WithOpaque(),
		)
		got := checkSpellContract([]*spells.Spell{full})
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "1 spell(s) satisfy the required mgs_ contract")
		require.Len(t, got.Details, 1)
		assert.Equal(t, "go: needs, provides, ignore-dirs, version-probe, language, opaque", got.Details[0])
	})

	// Sorted, because the details are a coverage report a reader scans by spell name.
	t.Run("details are sorted", func(t *testing.T) {
		got := checkSpellContract([]*spells.Spell{spells.NewSpell("zig"), spells.NewSpell("acme")})
		require.Len(t, got.Details, 2)
		assert.True(t, strings.HasPrefix(got.Details[0], "acme:"), got.Details)
		assert.True(t, strings.HasPrefix(got.Details[1], "zig:"), got.Details)
	})
}
