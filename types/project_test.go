package types

import (
	"fmt"
	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"testing"
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
	goSpell := spells.NewSpell("go",
		spells.WithSources("**/*.go"),
		spells.WithOutputs("bin/**"),
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
	pySpell := spells.NewSpell("python",
		spells.WithSources("**/*.py"),
		spells.WithOutputs("dist/**"),
	)
	p.AttachSpell(pySpell)
	assert.Equal(t, "go", p.Spell, "primary Spell must not change on second AttachSpell")
	assert.Len(t, p.Spells, 2)
}

// TestProjectDisplayNamePrefersDeclaredName pins the fix for the root project's
// label. Without a declared name it falls back to the checkout's directory
// basename, so a worktree, a renamed clone, or a CI checkout each renamed the
// ROOT project and rewrote every generated index that names it - which is why
// regenerating MAGUS.md from a worktree used to produce spurious diffs.
func TestProjectDisplayNamePrefersDeclaredName(t *testing.T) {
	// The root: path "." carries no name of its own, so the declared one is the
	// only thing that survives being checked out somewhere else.
	assert.Equal(t, "magus", ProjectDisplayName(".", "magus", "/tmp/some-worktree-name"),
		"a declared name must win over the directory basename")
	assert.Equal(t, "some-worktree-name", ProjectDisplayName(".", "", "/tmp/some-worktree-name"),
		"without one, the basename is still the fallback")

	// A nested project already has an unambiguous path, so nothing changes there.
	assert.Equal(t, "libs/foo", ProjectDisplayName("libs/foo", "", "/tmp/w/libs/foo"))
	assert.Equal(t, "custom", ProjectDisplayName("libs/foo", "custom", "/tmp/w/libs/foo"))
}

// benchProject builds a project declaring nOwn project-wide globs, nTarget per-target
// globs spread over 4 targets, and nInbound globs written in by 2 other projects.
func benchProject(nOwn, nTarget, nInbound int) *Project {
	p := &Project{Path: "api"}
	for i := 0; i < nOwn; i++ {
		p.Outputs = append(p.Outputs, fmt.Sprintf("dist/own-%d/**", i))
	}
	if nTarget > 0 {
		p.TargetOutputs = map[string][]OutputRef{}
		for i := 0; i < nTarget; i++ {
			t := fmt.Sprintf("target-%d", i%4)
			p.TargetOutputs[t] = append(p.TargetOutputs[t], OutputRef{Glob: fmt.Sprintf("gen/t-%d/**", i)})
		}
	}
	if nInbound > 0 {
		p.InboundOutputs = map[string][]string{}
		for i := 0; i < nInbound; i++ {
			w := fmt.Sprintf("writer-%d", i%2)
			p.InboundOutputs[w] = append(p.InboundOutputs[w], fmt.Sprintf("src/gen/in-%d.ts", i))
		}
	}
	return p
}

// BenchmarkProjectAllOutputs measures the per-project view every output consumer reads:
// `magus clean` calls it once per project, watch calls it once per project at startup,
// the merge driver calls it once per project per conflicted file, and FindOutputProducer
// calls it inside its own scan over all projects. The dedup is membership-tested against
// two growing slices, so cost is quadratic in the glob count - these sizes are what say
// whether that matters at realistic and pathological widths.
func BenchmarkProjectAllOutputs(b *testing.B) {
	cases := []struct {
		name                    string
		nOwn, nTarget, nInbound int
	}{
		{"bare/8-own", 8, 0, 0},
		{"typical/8-own+8-target", 8, 8, 0},
		{"cross/8-own+8-target+4-inbound", 8, 8, 4},
		{"wide/64-own+64-target+32-inbound", 64, 64, 32},
	}
	for _, tc := range cases {
		p := benchProject(tc.nOwn, tc.nTarget, tc.nInbound)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = p.AllOutputs()
			}
		})
	}
}
