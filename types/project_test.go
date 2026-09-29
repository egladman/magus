package types

import (
	"fmt"
	"slices"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAttachSpellSkipsInternalForThePrimarySlot pins the rule that keeps
// `magus ls` informative. Every project is DISCOVERED by having a magusfile, so
// the magusfile registration attaches to all of them and, being first, used to
// claim the primary slot everywhere: `magus ls` answered "spell: magusfile" for
// 9 of this repo's 10 projects, which is true by construction and so tells a
// reader nothing while hiding the toolchain they wanted.
func TestAttachSpellSkipsInternalForThePrimarySlot(t *testing.T) {
	t.Parallel()
	internal := spells.NewSpell("magusfile", spells.WithInternal())
	toolchain := spells.NewSpell("go")

	p := &Project{}
	p.AttachSpell(internal)
	assert.Empty(t, p.Spell, "plumbing must not claim the primary slot")

	p.AttachSpell(toolchain)
	assert.Equal(t, "go", p.Spell, "the first real toolchain does")

	// Both are still BOUND: only the display slot is affected, so dispatch through
	// the magusfile registration is untouched.
	assert.Equal(t, []string{"magusfile", "go"}, p.Spells)
}

// TestAttachSpellLeavesNoPrimaryWhenOnlyInternal covers a project whose targets
// all come from its magusfile: it genuinely has no toolchain spell, and saying so
// beats naming one it does not have.
func TestAttachSpellLeavesNoPrimaryWhenOnlyInternal(t *testing.T) {
	t.Parallel()
	p := &Project{}
	p.AttachSpell(spells.NewSpell("magusfile", spells.WithInternal()))
	assert.Empty(t, p.Spell)
	assert.Equal(t, []string{"magusfile"}, p.Spells)
}

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

// TestDeclaredGlobsRootsAReachingSourceAndCollapsesTheDuplicate covers the two things
// cleaning the rooting does at once. A project-wide "../proto/**" resolves against the
// declaring project rather than concatenating into "api/../proto/**", which nothing
// matches; and so resolved, it is the SAME declaration as the per-target ctx.readsFiles
// spelling of it, so the two collapse to one entry rather than double-counting the same
// input. Dedup here is string equality on the rooted form, which is why both sides have
// to root the same way for it to converge.
func TestDeclaredGlobsRootsAReachingSourceAndCollapsesTheDuplicate(t *testing.T) {
	t.Parallel()
	p := &Project{
		Path:         "api",
		Sources:      []string{"**/*.go", "../proto/**"},
		TargetInputs: map[string][]InputRef{"build": {{Project: "proto", Glob: "**"}}},
	}

	assert.Equal(t, []string{"api/**/*.go", "proto/**"}, p.DeclaredGlobs())
}

func TestMatchesAnyGlobExclusions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		globs []string
		path  string
		want  bool
	}{
		{"an exclusion narrows the glob before it", []string{"gen/*.go", "!gen/runtime.go"}, "gen/runtime.go", false},
		{"the rest of the glob still matches", []string{"gen/*.go", "!gen/runtime.go"}, "gen/fs.go", true},
		{"an exclusion is a glob too", []string{"gen/*.go", "!gen/*_test.go"}, "gen/fs_test.go", false},
		{"one exclusion run narrows every glob before it", []string{"a/*", "b/*", "!*/x", "!*/y"}, "a/y", false},
		{"a glob after an exclusion starts a fresh run", []string{"gen/*.go", "!gen/runtime.go", "gen/runtime.go"}, "gen/runtime.go", true},
		{"an exclusion never reaches back past an earlier one", []string{"a/*", "!a/x", "b/*", "!a/*"}, "a/y", true},
		{"a leading exclusion narrows nothing", []string{"!gen/fs.go", "gen/*.go"}, "gen/fs.go", true},
		{"only exclusions match nothing", []string{"!gen/fs.go"}, "gen/fs.go", false},
		{"an escaped bang is a literal leading bang", []string{`\!gen.go`}, "!gen.go", true},
		{"the escaped form is not an exclusion", []string{"*.go", `\!x.go`}, "x.go", true},
	} {
		assert.Equal(t, tc.want, MatchesAnyGlob(tc.globs, tc.path), tc.name)
	}
}

func TestCheckExclusions(t *testing.T) {
	t.Parallel()
	require.NoError(t, CheckExclusions([]string{"gen/*.go", "!gen/runtime.go"}))
	require.NoError(t, CheckExclusions([]string{`\!literal`}), "an escaped bang is an ordinary glob")
	require.NoError(t, CheckExclusions(nil))

	err := CheckExclusions([]string{"!gen/runtime.go", "!gen/*_test.go"})
	require.ErrorContains(t, err, "every glob in")
	err = CheckExclusions([]string{"!gen/runtime.go", "gen/*.go"})
	require.ErrorContains(t, err, `exclusion "!gen/runtime.go" comes before any glob it could narrow`)
	err = CheckExclusions([]string{"gen/*.go", "!"})
	require.ErrorContains(t, err, "names no pattern")
}

func TestRootGlobKeepsTheExclusion(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "!api/gen/runtime.go", RootGlob("api", "!gen/runtime.go"))
	assert.Equal(t, "!proto/x", RootGlob("docs", "!../proto/x"))
	assert.Equal(t, "!gen/x", RootGlob(".", "!./gen/x"))
}

func TestInvalidGlobsJudgesAnExclusionByItsPattern(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"![bad"}, InvalidGlobs([]string{"gen/*", "!gen/x", "![bad"}))
}

// TestUnionGlobsKeepsEachDeclarationsExclusions pins why a flattened union cannot be a
// plain concatenation: the second declaration's exclusion would join the first one's
// run and carve its file out of a declaration that never excluded it.
func TestUnionGlobsKeepsEachDeclarationsExclusions(t *testing.T) {
	t.Parallel()
	sources := []string{"**/*.go"}
	outputs := []string{"gen/*.go", "!gen/runtime.go"}

	require.False(t, MatchesAnyGlob(slices.Concat(sources, outputs), "gen/runtime.go"), "the concatenation loses the path")

	union := UnionGlobs(sources, outputs)
	assert.Equal(t, []string{"gen/*.go", "!gen/runtime.go", "**/*.go"}, union)
	assert.True(t, MatchesAnyGlob(union, "gen/runtime.go"))
	assert.False(t, MatchesAnyGlob(UnionGlobs(outputs), "gen/runtime.go"))
	assert.Equal(t, []string{"a", "b"}, UnionGlobs([]string{"a", "b"}, []string{"b", "a"}), "plain globs dedup")
	assert.Equal(t, []string{"a", "!b", "c", "!d"}, UnionGlobs([]string{"!x", "a", "!b"}, []string{"c", "!d"}),
		"a leading exclusion is dropped rather than joining the run before it")
}

func TestDeclaredGlobsAndAllOutputsKeepAnExclusionWithItsDeclaration(t *testing.T) {
	t.Parallel()
	p := &Project{
		Path:    "api",
		Sources: []string{"**/*.go"},
		Outputs: []string{"dist/**"},
		TargetOutputs: map[string][]OutputRef{
			"bindings": {{Glob: "gen/*.go"}, {Glob: "!gen/runtime.go"}},
			"docs":     {{Glob: "MAGUS.md"}},
		},
	}

	assert.Equal(t, []string{"gen/*.go", "!gen/runtime.go", "dist/**", "MAGUS.md"}, p.AllOutputs())
	declared := p.DeclaredGlobs()
	assert.Equal(t, []string{
		"api/gen/*.go", "!api/gen/runtime.go", "api/**/*.go", "api/MAGUS.md", "api/dist/**",
	}, declared, "the declaration AllOutputs and TargetOutputs both carry appears once")
	assert.True(t, MatchesAnyGlob(declared, "api/gen/runtime.go"), "the project's sources still declare it")
	assert.False(t, MatchesAnyGlob(UnionGlobs(p.AllOutputs()), "gen/runtime.go"), "but no output does")
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
// ROOT project and rewrote every generated index that names it, which is why
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
// two growing slices, so cost is quadratic in the glob count: these sizes are what say
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
