package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProjectAllOutputs(t *testing.T) {
	// No per-target outputs: AllOutputs is exactly the project-wide set.
	p := &Project{Outputs: []string{"dist/**"}}
	assert.Equal(t, []string{"dist/**"}, p.AllOutputs())

	// Per-target outputs union in, deduped against project-wide, sorted for determinism.
	p = &Project{
		Outputs: []string{"dist/**"},
		TargetOutputs: map[string][]OutputRef{
			"docs":     {{Glob: "docs/*.md"}, {Glob: "dist/**"}}, // dist/** duplicates project-wide -> dropped
			"generate": {{Glob: "MAGUS.md"}},
		},
	}
	assert.Equal(t, []string{"dist/**", "MAGUS.md", "docs/*.md"}, p.AllOutputs())
}

func TestProjectLabel(t *testing.T) {
	t.Parallel()
	// Display form: bare paths (the scheme is metadata, not display content).
	assert.Equal(t, "api", ProjectLabel("api", "/repo/api"))
	assert.Equal(t, "web/studio", ProjectLabel("web/studio", "/repo/web/studio"))
	// Root project: path "." / "" resolves to the dir basename, never a bare ".".
	assert.Equal(t, "magus", ProjectLabel(".", "/home/user/magus"))
	assert.Equal(t, "magus", ProjectLabel("", "/home/user/magus"))
	// No usable dir falls back to the readable sentinel, never "" or ".".
	assert.Equal(t, "(workspace root)", ProjectLabel(".", ""))
	assert.Equal(t, "(workspace root)", ProjectLabel("", "."))
}

func TestWorkspaceRef(t *testing.T) {
	t.Parallel()
	// Machine form: the scheme is always present (this is what users pipe
	// back into commands). Empty paths resolve to the root.
	assert.Equal(t, "workspace://.", WorkspaceRef(""))
	assert.Equal(t, "workspace://.", WorkspaceRef("."))
	assert.Equal(t, "workspace://pkg/foo", WorkspaceRef("pkg/foo"))
}

func TestProjectRef(t *testing.T) {
	t.Parallel()
	// One struct, two render forms. Display is the human form (same as the
	// ProjectLabel helper above); WorkspaceURI is the machine form.
	root := NewProjectRef(".", "/home/user/magus")
	assert.Equal(t, ".", root.Path)
	assert.Equal(t, "magus", root.Display(), "root display uses the dir basename, never a bare '.'")
	assert.Equal(t, "workspace://.", root.WorkspaceURI())

	nested := NewProjectRef("pkg/foo", "/home/user/magus/pkg/foo")
	assert.Equal(t, "pkg/foo", nested.Display())
	assert.Equal(t, "workspace://pkg/foo", nested.WorkspaceURI())
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
