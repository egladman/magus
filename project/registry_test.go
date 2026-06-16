package project

import (
	"testing"

	"github.com/egladman/magus/types"
)

func TestNewSpellRegistry_Empty(t *testing.T) {
	r := NewSpellRegistry()
	if r == nil {
		t.Fatal("NewSpellRegistry returned nil")
	}
	if len(r.All()) != 0 {
		t.Error("new registry should be empty")
	}
}

func TestSpellRegistry_RegisterAndLookup(t *testing.T) {
	r := NewSpellRegistry()
	s := types.NewSpell("myspell")
	r.RegisterSpell(s)

	got, ok := r.Lookup("myspell")
	if !ok {
		t.Fatal("Lookup: spell not found after Register")
	}
	if got.Name() != "myspell" {
		t.Errorf("Lookup: Name() = %q, want %q", got.Name(), "myspell")
	}
}

func TestSpellRegistry_Unregister(t *testing.T) {
	r := NewSpellRegistry()
	s := types.NewSpell("gone")
	r.RegisterSpell(s)
	r.UnregisterSpell("gone")

	if _, ok := r.Lookup("gone"); ok {
		t.Error("spell still found after Unregister")
	}
}

func TestDefaultSpellRegistry_NonNil(t *testing.T) {
	if DefaultSpellRegistry() == nil {
		t.Error("DefaultSpellRegistry() returned nil")
	}
}
