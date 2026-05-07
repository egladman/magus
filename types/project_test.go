package types_test

import (
	"testing"

	"github.com/egladman/magus/types"
)

func TestTargetPolicy_IsZero(t *testing.T) {
	cases := []struct {
		name   string
		p      types.TargetPolicy
		isZero bool
	}{
		{"zero value", types.TargetPolicy{}, true},
		{"CheckClean set", types.TargetPolicy{CheckClean: true}, false},
		{"TrackFlake set", types.TargetPolicy{TrackFlake: true}, false},
		{"both set", types.TargetPolicy{CheckClean: true, TrackFlake: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.p.IsZero()
			if got != tc.isZero {
				t.Errorf("IsZero() = %v, want %v", got, tc.isZero)
			}
		})
	}
}

func TestProject_AttachSpell(t *testing.T) {
	goSpell := types.NewSpell("go",
		types.WithSources("**/*.go"),
		types.WithSpellOutputs("bin/**"),
	)

	p := &types.Project{Path: "api/"}
	p.AttachSpell(goSpell)

	if p.Spell != "go" {
		t.Errorf("Spell = %q, want %q", p.Spell, "go")
	}
	if len(p.Spells) != 1 || p.Spells[0] != "go" {
		t.Errorf("Spells = %v, want [go]", p.Spells)
	}
	if len(p.Bindings) != 1 || p.Bindings[0].Name != "go" {
		t.Errorf("Bindings = %v, want [{Name:go}]", p.Bindings)
	}
	if len(p.Sources) == 0 {
		t.Error("Sources should be populated after AttachSpell")
	}
	if len(p.Outputs) == 0 {
		t.Error("Outputs should be populated after AttachSpell")
	}

	// Attaching a second spell must NOT overwrite the primary Spell field.
	pySpell := types.NewSpell("python",
		types.WithSources("**/*.py"),
		types.WithSpellOutputs("dist/**"),
	)
	p.AttachSpell(pySpell)
	if p.Spell != "go" {
		t.Errorf("Spell changed to %q on second AttachSpell, want %q", p.Spell, "go")
	}
	if len(p.Spells) != 2 {
		t.Errorf("len(Spells) = %d, want 2", len(p.Spells))
	}
}
