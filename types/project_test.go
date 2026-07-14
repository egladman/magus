package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProjectLabel(t *testing.T) {
	t.Parallel()
	// Non-root paths pass through unchanged.
	assert.Equal(t, "api", ProjectLabel("api", "/repo/api"))
	assert.Equal(t, "web/studio", ProjectLabel("web/studio", "/repo/web/studio"))
	// Root project (path "" or ".") collapses to the workspace dir's base name.
	assert.Equal(t, "magus", ProjectLabel(".", "/home/user/magus"))
	assert.Equal(t, "magus", ProjectLabel("", "/home/user/magus"))
	// No usable dir falls back to a readable placeholder, never "" or ".".
	assert.Equal(t, "(workspace root)", ProjectLabel(".", ""))
	assert.Equal(t, "(workspace root)", ProjectLabel("", "."))
}

func TestProjectRef(t *testing.T) {
	t.Parallel()
	// Root project: path and name diverge, so Display carries both explicitly.
	root := NewProjectRef(".", "/home/user/magus")
	assert.Equal(t, ".", root.Path)
	assert.Equal(t, "magus", root.Name)
	assert.Equal(t, "magus (.)", root.Display(), "the root shows name and path, never a bare '.'")

	// Nested project: path == name, so Display shows it once.
	nested := NewProjectRef("pkg/foo", "/home/user/magus/pkg/foo")
	assert.Equal(t, "pkg/foo", nested.Name)
	assert.Equal(t, "pkg/foo", nested.Display())
}

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
