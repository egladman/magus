package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/spells"
)

func TestNewSpellRegistry_Empty(t *testing.T) {
	r := NewSpellRegistry()
	require.NotNil(t, r)
	assert.Empty(t, r.All(), "new registry should be empty")
}

func TestSpellRegistry_RegisterAndLookup(t *testing.T) {
	r := NewSpellRegistry()
	s := spells.NewSpell("myspell")
	r.RegisterSpell(s)

	got, ok := r.Lookup("myspell")
	require.True(t, ok, "Lookup: spell not found after Register")
	assert.Equal(t, "myspell", got.Name())
}

func TestSpellRegistry_Unregister(t *testing.T) {
	r := NewSpellRegistry()
	s := spells.NewSpell("gone")
	r.RegisterSpell(s)
	r.UnregisterSpell("gone")

	_, ok := r.Lookup("gone")
	assert.False(t, ok, "spell still found after Unregister")
}

func TestSpellRegistry_ReplaceSpell(t *testing.T) {
	r := NewSpellRegistry()
	embedded := spells.NewSpell("go", spells.WithTargets("build"))
	r.RegisterSpell(embedded)
	r.RegisterSpell(spells.NewSpell("rust"))

	override := spells.NewSpell("go", spells.WithTargets("build", "vet"))
	assert.True(t, r.ReplaceSpell(override))
	got, ok := r.Lookup("go")
	require.True(t, ok)
	assert.Same(t, override, got)
	assert.Equal(t, []string{"go", "rust"}, spellNames(r.All()), "the entry keeps its place")

	assert.False(t, r.ReplaceSpell(spells.NewSpell("python")), "a name nothing holds is added")
	assert.Equal(t, []string{"go", "rust", "python"}, spellNames(r.All()))
	assert.False(t, r.ReplaceSpell(nil))
}

func spellNames(all []*spells.Spell) []string {
	out := make([]string, len(all))
	for i, s := range all {
		out[i] = s.Name()
	}
	return out
}

func TestDefaultSpellRegistry_NonNil(t *testing.T) {
	assert.NotNil(t, DefaultSpellRegistry())
}
