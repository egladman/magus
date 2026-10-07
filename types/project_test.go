package types

import (
	"fmt"
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
	p := &Project{Outputs: MustParseGlobs("dist/**")}
	assert.Equal(t, MustParseGlobs("dist/**"), p.AllOutputs())

	// Per-target outputs union in, deduped against project-wide, sorted for determinism.
	p = &Project{
		Outputs: MustParseGlobs("dist/**"),
		TargetOutputs: map[string][]OutputRef{
			"docs":     {{Glob: "docs/*.md"}, {Glob: "dist/**"}}, // dist/** duplicates project-wide -> dropped
			"generate": {{Glob: "MAGUS.md"}},
		},
	}
	assert.Equal(t, MustParseGlobs("MAGUS.md", "dist/**", "docs/*.md"), p.AllOutputs())
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
		Sources:      MustParseGlobs("**/*.go", "../proto/**"),
		TargetInputs: map[string][]InputRef{"build": {{Project: "proto", Glob: "**"}}},
	}

	assert.Equal(t, MustParseGlobs("api/**/*.go", "proto/**"), p.DeclaredGlobs())
}

func TestGlobMatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		glob Glob
		path string
		want bool
	}{
		{"an exclusion narrows the pattern", Glob{"gen/*.go", []string{"gen/runtime.go"}}, "gen/runtime.go", false},
		{"the rest of the pattern still matches", Glob{"gen/*.go", []string{"gen/runtime.go"}}, "gen/fs.go", true},
		{"an exclusion is a glob too", Glob{"gen/*.go", []string{"gen/*_test.go"}}, "gen/fs_test.go", false},
		{"a literal directory claims what is beneath it", Glob{Pattern: "dist"}, "dist/app/main.js", true},
		{"a literal directory is not a prefix match", Glob{Pattern: "dist"}, "distro/a.js", false},
		{"a literal directory exclusion carves out what is beneath it", Glob{"dist", []string{"dist/vendor"}}, "dist/vendor/lib.js", false},
		{"a literal directory exclusion keeps its siblings", Glob{"dist", []string{"dist/vendor"}}, "dist/app.js", true},
		{"an escaped bang is a literal leading bang", Glob{Pattern: `\!gen.go`}, "!gen.go", true},
		{"an unparsable pattern matches nothing", Glob{Pattern: "[bad"}, "[bad", false},
	} {
		assert.Equal(t, tc.want, tc.glob.Match(tc.path), tc.name)
	}
}

// TestParseGlobsIsOrderFreeWithinOneCall pins the one meaning of "!": every exclusion in
// a call narrows every pattern in that call, whichever comes first.
func TestParseGlobsIsOrderFreeWithinOneCall(t *testing.T) {
	t.Parallel()
	want := []Glob{
		{Pattern: "a/*", Except: []string{"*/x", "*/y"}},
		{Pattern: "b/*", Except: []string{"*/x", "*/y"}},
	}
	for _, call := range [][]string{
		{"a/*", "b/*", "!*/x", "!*/y"},
		{"!*/y", "a/*", "!*/x", "b/*"},
	} {
		got, err := ParseGlobs(call)
		require.NoError(t, err)
		assert.Equal(t, want, got, "%q", call)
	}

	got, err := ParseGlobs([]string{"*.go", `\!x.go`})
	require.NoError(t, err)
	assert.Equal(t, []Glob{{Pattern: "*.go"}, {Pattern: `\!x.go`}}, got, "an escaped bang is a pattern, not an exclusion")
}

func TestParseGlobsRefusesADeclarationOfNothing(t *testing.T) {
	t.Parallel()
	_, err := ParseGlobs([]string{"!gen/runtime.go", "!gen/*_test.go"})
	require.EqualError(t, err, `exclusion "!gen/runtime.go" has no glob to narrow`)
	_, err = ParseGlobs([]string{"gen/*.go", "!"})
	require.EqualError(t, err, `glob "!" names no pattern`)
	_, err = ParseGlobs([]string{""})
	require.EqualError(t, err, `glob "" names no pattern`)

	got, err := ParseGlobs(nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestGlobRootRootsItsExclusions(t *testing.T) {
	t.Parallel()
	assert.Equal(t, Glob{"api/gen/*.go", []string{"api/gen/runtime.go"}},
		Glob{"gen/*.go", []string{"gen/runtime.go"}}.Root("api"))
	assert.Equal(t, Glob{"proto/**", []string{"proto/x"}}, Glob{"../proto/**", []string{"../proto/x"}}.Root("docs"))
	assert.Equal(t, Glob{Pattern: "gen/x"}, Glob{Pattern: "./gen/x"}.Root("."))
}

func TestInvalidGlobsJudgesExclusionsToo(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"[bad"}, InvalidGlobs([]Glob{{"gen/*", []string{"gen/x", "[bad"}}, {Pattern: "[bad"}}))
}

// TestAGlobsExclusionsNeverReachAnotherDeclaration pins why a union of declarations is a
// plain union: one declaration's exclusion carves nothing out of another's pattern.
func TestAGlobsExclusionsNeverReachAnotherDeclaration(t *testing.T) {
	t.Parallel()
	outputs, err := ParseGlobs([]string{"gen/*.go", "!gen/runtime.go"})
	require.NoError(t, err)
	union := append(MustParseGlobs("**/*.go"), outputs...)

	assert.True(t, MatchGlobs(union, "gen/runtime.go"))
	assert.False(t, MatchGlobs(outputs, "gen/runtime.go"))
	assert.Equal(t, []Glob{{Pattern: "a"}, {Pattern: "b"}}, CompactGlobs([]Glob{{Pattern: "b"}, {Pattern: "a"}, {Pattern: "b"}}),
		"a union dedups")
}

func TestDeclaredGlobsAndAllOutputsKeepAnExclusionWithItsDeclaration(t *testing.T) {
	t.Parallel()
	p := &Project{
		Path:    "api",
		Sources: MustParseGlobs("**/*.go"),
		Outputs: MustParseGlobs("dist/**"),
		TargetOutputs: map[string][]OutputRef{
			"bindings": {{Glob: "gen/*.go", Except: []string{"gen/runtime.go"}}},
			"docs":     {{Glob: "MAGUS.md"}},
		},
	}

	bindings := Glob{"gen/*.go", []string{"gen/runtime.go"}}
	assert.Equal(t, []Glob{{Pattern: "MAGUS.md"}, {Pattern: "dist/**"}, bindings}, p.AllOutputs())
	declared := p.DeclaredGlobs()
	assert.Equal(t, []Glob{
		{Pattern: "api/**/*.go"}, {Pattern: "api/MAGUS.md"}, {Pattern: "api/dist/**"}, bindings.Root("api"),
	}, declared, "the declaration AllOutputs and TargetOutputs both carry appears once")
	assert.True(t, MatchGlobs(declared, "api/gen/runtime.go"), "the project's sources still declare it")
	assert.False(t, MatchGlobs(p.AllOutputs(), "gen/runtime.go"), "but no output does")
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
		p.Outputs = append(p.Outputs, Glob{Pattern: fmt.Sprintf("dist/own-%d/**", i)})
	}
	if nTarget > 0 {
		p.TargetOutputs = map[string][]OutputRef{}
		for i := 0; i < nTarget; i++ {
			t := fmt.Sprintf("target-%d", i%4)
			p.TargetOutputs[t] = append(p.TargetOutputs[t], OutputRef{Glob: fmt.Sprintf("gen/t-%d/**", i)})
		}
	}
	if nInbound > 0 {
		p.InboundOutputs = map[string][]Glob{}
		for i := 0; i < nInbound; i++ {
			w := fmt.Sprintf("writer-%d", i%2)
			p.InboundOutputs[w] = append(p.InboundOutputs[w], Glob{Pattern: fmt.Sprintf("src/gen/in-%d.ts", i)})
		}
	}
	return p
}

// BenchmarkProjectAllOutputs measures the per-project view every output consumer reads:
// `magus clean` calls it once per project, watch calls it once per project at startup,
// the merge driver calls it once per project per conflicted file, and FindOutputProducer
// calls it inside its own scan over all projects. The dedup sorts, so cost is n log n in
// the glob count: these sizes are what say
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

func TestCheckLayer(t *testing.T) {
	for _, ok := range [][2]string{
		{"internal/handler", "handler"},
		{"internal/handler/**", "handler"},
		{".", "composition"},
		{"cmd/*", "cli-2"},
	} {
		require.NoErrorf(t, CheckLayer(ok[0], ok[1]), "%v", ok)
	}
	for _, tc := range []struct{ dir, name, want string }{
		{"", "handler", "blank directory"},
		{"/abs/path", "handler", "is absolute"},
		{"../sibling", "handler", "escapes the workspace root"},
		{"internal/./handler", "handler", `write "internal/handler"`},
		{"internal/", "handler", `write "internal"`},
		{"internal/[", "handler", "not a valid glob"},
		{"internal", "", "lowercase slug"},
		{"internal", "Handler", "lowercase slug"},
		{"internal", "hand ler", "lowercase slug"},
	} {
		err := CheckLayer(tc.dir, tc.name)
		require.Errorf(t, err, "%q -> %q", tc.dir, tc.name)
		assert.ErrorContains(t, err, tc.want)
		assert.ErrorIs(t, err, LayerDeclarationInvalid)
	}
}

func TestLayerFor(t *testing.T) {
	layers := map[string]string{
		"internal/**":         "engine",
		"internal/handler/**": "handler",
		"internal/handler":    "transport",
		"cmd/*":               "cli",
	}
	for dir, want := range map[string]string{
		"internal/cache":       "engine",
		"internal/handler/mcp": "handler",
		"internal/handler":     "transport",
		"cmd/magus":            "cli",
		"internal":             "engine",
	} {
		got, ok := ResolveLayer(layers, dir)
		assert.Truef(t, ok, dir)
		assert.Equalf(t, want, got, dir)
	}
	for _, dir := range []string{"cmd", "cmd/magus/sub", "types", "."} {
		_, ok := ResolveLayer(layers, dir)
		assert.Falsef(t, ok, "%s is covered by no declaration", dir)
	}
	_, ok := ResolveLayer(map[string]string{"internal": "engine"}, "internal/cache")
	assert.False(t, ok, "an exact path names one directory, not its subtree")
}

func TestGlobsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"docs/changelog.md", "**/*.md", true},
		{"docs/changelog.md", "docs/changelog.md", true},
		{"docs/changelog.md", "CHANGELOG.md", false},
		{"proto/gen/descriptor.binpb", "gen/*.json", false},
		{"gen/*.json", "**/*.md", false},
		{"gen/*.json", "gen/**", true},
		{"reference/buzz/*.md", "**/*.md", true},
		{"docs/**", "docs/gen/site/index.html", true},
		{"a/*.md", "b/*.md", false},
		{"**", "anything/at/all.txt", true},
		{"dist", "dist/**", true},
		{"dist/a.js", "dist/b.js", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, GlobsOverlap(tc.a, tc.b), "GlobsOverlap(%q, %q)", tc.a, tc.b)
		assert.Equal(t, tc.want, GlobsOverlap(tc.b, tc.a), "GlobsOverlap(%q, %q)", tc.b, tc.a)
	}
}
