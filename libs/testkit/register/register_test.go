package register

import (
	"context"
	"strings"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// TestSpellBindsThroughWithSpell: the returned name is the one magus.WithSpell
// looks up, and the spell it finds runs the invoker it was registered with.
func TestSpellBindsThroughWithSpell(t *testing.T) {
	var ran []string
	name := Spell(t, "probe",
		spells.WithTargets("build", "test"),
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			ran = append(ran, req.Target)
			return nil, nil
		}),
	)

	p := &types.Project{Path: "."}
	require.NoError(t, magus.WithSpell(name)(p))
	assert.Equal(t, &types.Project{
		Path:     ".",
		Spell:    name,
		Spells:   []string{name},
		Bindings: []*types.Binding{{Name: name}},
	}, p)

	sp, ok := project.DefaultSpellRegistry().Lookup(name)
	require.True(t, ok)
	assert.Equal(t, []string{"build", "test"}, sp.Targets())
	_, err := sp.Invoke(t.Context(), spells.InvokeRequest{Target: "test"})
	require.NoError(t, err)
	assert.Equal(t, []string{"test"}, ran)
}

// TestSpellNamesAreUnique: the registry panics on a duplicate name, so two parallel
// tests registering "probe" must get different names.
func TestSpellNamesAreUnique(t *testing.T) {
	a := Spell(t, "probe")
	b := Spell(t, "probe")

	assert.NotEqual(t, a, b)
	assert.True(t, strings.HasPrefix(a, "probe-"), a)
	assert.True(t, strings.HasPrefix(b, "probe-"), b)
}

func TestSpellUnregistersWhenTheTestEnds(t *testing.T) {
	var name string
	t.Run("registers", func(t *testing.T) {
		name = Spell(t, "probe")
		_, ok := project.DefaultSpellRegistry().Lookup(name)
		assert.True(t, ok, "%s is not registered during the test", name)
	})

	_, ok := project.DefaultSpellRegistry().Lookup(name)
	assert.False(t, ok, "%s outlived the test that registered it", name)
	assert.ErrorIs(t, magus.WithSpell(name)(&types.Project{Path: "."}), types.ErrSpellNotRegistered)
}
