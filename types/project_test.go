package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProject_AttachSpell(t *testing.T) {
	goSpell := NewSpell("go",
		WithSources("**/*.go"),
		WithSpellOutputs("bin/**"),
	)

	p := &Project{Path: "api/"}
	p.AttachSpell(goSpell)

	assert.Equal(t, "go", p.Spell)
	assert.Equal(t, []string{"go"}, p.Spells)
	assert.Len(t, p.Bindings, 1)
	assert.Equal(t, "go", p.Bindings[0].Name)
	assert.NotEmpty(t, p.Sources, "Sources should be populated after AttachSpell")
	assert.NotEmpty(t, p.Outputs, "Outputs should be populated after AttachSpell")

	// Attaching a second spell must NOT overwrite the primary Spell field.
	pySpell := NewSpell("python",
		WithSources("**/*.py"),
		WithSpellOutputs("dist/**"),
	)
	p.AttachSpell(pySpell)
	assert.Equal(t, "go", p.Spell, "primary Spell must not change on second AttachSpell")
	assert.Len(t, p.Spells, 2)
}
