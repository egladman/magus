package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestNewSpellRegistry_Empty(t *testing.T) {
	r := NewSpellRegistry()
	require.NotNil(t, r)
	assert.Empty(t, r.All(), "new registry should be empty")
}

func TestSpellRegistry_RegisterAndLookup(t *testing.T) {
	r := NewSpellRegistry()
	s := types.NewSpell("myspell")
	r.RegisterSpell(s)

	got, ok := r.Lookup("myspell")
	require.True(t, ok, "Lookup: spell not found after Register")
	assert.Equal(t, "myspell", got.Name())
}

func TestSpellRegistry_Unregister(t *testing.T) {
	r := NewSpellRegistry()
	s := types.NewSpell("gone")
	r.RegisterSpell(s)
	r.UnregisterSpell("gone")

	_, ok := r.Lookup("gone")
	assert.False(t, ok, "spell still found after Unregister")
}

func TestDefaultSpellRegistry_NonNil(t *testing.T) {
	assert.NotNil(t, DefaultSpellRegistry())
}
