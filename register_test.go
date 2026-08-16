package magus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

func init() {
	// Register minimal test spells so WithSpell name lookups resolve without
	// linking interp/bindings into the library's test binary. (There is no import
	// cycle — bindings does not import this package; the point is to keep the
	// library free of the Buzz VM. bindings' init also eagerly registers the real
	// built-in spells, which would collide with these shims.)
	for _, meta := range []struct {
		name    string
		sources []string
	}{
		{"go", []string{"**/*.go", "go.mod", "go.sum"}},
		{"rust", []string{"**/*.rs", "Cargo.toml"}},
		{"ts", []string{"**/*.ts", "**/*.tsx", "package.json"}},
		{"json", []string{"**/*.json", "**/*.jsonc"}},
	} {
		m := meta
		project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(
			m.name,
			spells.WithSources(m.sources...),
		))
	}
}

// makeWorkspaceRoot lays down project manifest stubs and returns the
// temp directory path. Does not call Open — lets the caller register
// options first.
func makeWorkspaceRoot(t *testing.T, manifests ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range manifests {
		abs := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(""), 0o644))
	}
	return root
}

// TestWithDependsOnRelativeSibling verifies that WithDependsOn("../api") from
// "extensions/drape" resolves to "extensions/api" (one level up).
func TestWithDependsOnRelativeSibling(t *testing.T) {
	root := makeWorkspaceRoot(
		t,
		"magusfile.buzz",                  // project "."
		"extensions/api/magusfile.buzz",   // project "extensions/api"
		"extensions/drape/magusfile.buzz", // project "extensions/drape"
	)

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("extensions/drape", WithDependsOn("../api"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)
	p := ws.Get("extensions/drape")
	require.NotNil(t, p, "project extensions/drape not found")
	assert.Contains(t, p.DependsOn, "extensions/api")
}

// TestWithDependsOnRelativeUpTwo verifies "../../../" style paths resolve correctly.
func TestWithDependsOnRelativeUpTwo(t *testing.T) {
	root := makeWorkspaceRoot(
		t,
		"magusfile.buzz",
		"a/b/c/magusfile.buzz",
	)

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("a/b/c", WithDependsOn("../../.."))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)
	p := ws.Get("a/b/c")
	require.NotNil(t, p, "project a/b/c not found")
	assert.Contains(t, p.DependsOn, ".")
}

// TestWithDependsOnBarePathUnchanged verifies that a bare repo-relative
// path (no dots, no slashes) is returned unchanged.
func TestWithDependsOnBarePathUnchanged(t *testing.T) {
	root := makeWorkspaceRoot(
		t,
		"magusfile.buzz",
		"api/magusfile.buzz",
	)

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithDependsOn("api"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)
	p := ws.Get(".")
	require.NotNil(t, p, "project . not found")
	assert.Contains(t, p.DependsOn, "api")
}

// TestWithDependsOnEscapesRoot verifies that a relative path that
// would escape the workspace root is rejected.
func TestWithDependsOnEscapesRoot(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithDependsOn("../outside"))

	_, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	assert.Error(t, err, "Inspect: expected error for path escaping workspace root")
}

// TestWithSpellAddsLanguage verifies that WithSpell(name) populates
// both the Spell and Spells fields via Register.
func TestWithSpellAddsLanguage(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("//go:build magus\npackage main\n"), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("go"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)
	p := ws.Get(".")
	require.NotNil(t, p, "project . not discovered")
	assert.Equal(t, "go", p.Spell)
	assert.Equal(t, []string{"go"}, p.Spells)
}

// TestWithSpellMultipleTools verifies that calling WithSpell twice registers
// two tools and that both appear in p.Spells in registration order.
func TestWithSpellMultipleTools(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("go"), WithSpell("rust"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	p := ws.Get(".")
	require.NotNil(t, p, "project . not found")
	assert.Equal(t, "go", p.Spell, "Spell (primary)")
	assert.Equal(t, []string{"go", "rust"}, p.Spells)
	assert.Len(t, p.ResolvedSpells, 2)
}

// TestWithSpellUnknownTool verifies that WithSpell("nope") errors
// out at Open time rather than silently doing nothing.
func TestWithSpellUnknownTool(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("//go:build magus\npackage main\n"), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("nope"))

	_, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	assert.ErrorIs(t, err, types.ErrSpellNotRegistered, "Inspect: expected error for unknown tool")
}

// TestWithExclusiveOption verifies that WithExclusive() sets p.Exclusive.
func TestWithExclusiveOption(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithExclusive())

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	p := ws.Get(".")
	require.NotNil(t, p, "project . not found")
	assert.True(t, p.Exclusive, "Exclusive = false, want true")
}

// TestApplyIdempotent verifies that calling Inspect twice with the same registry
// does not double-accumulate Deps. Each Open gets a fresh *Workspace, so the
// registry applies cleanly regardless of how many times Inspect is called.
func TestApplyIdempotent(t *testing.T) {
	root := makeWorkspaceRoot(
		t,
		"magusfile.buzz",
		"api/magusfile.buzz",
	)

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithDependsOn("api"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)
	p := ws.Get(".")
	require.NotNil(t, p, "project . not found")

	countDep := func(deps []string, target string) int {
		n := 0
		for _, d := range deps {
			if d == target {
				n++
			}
		}
		return n
	}

	require.Equalf(t, 1, countDep(p.DependsOn, "api"), "after first Open: Deps = %v", p.DependsOn)

	// A second Inspect with the same registry must not double the deps;
	// each *Workspace is distinct.
	ws2, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "second Open")
	p2 := ws2.Get(".")
	require.NotNil(t, p2, "project . not found in second workspace")
	require.Equalf(t, 1, countDep(p2.DependsOn, "api"), "after second Open: Deps = %v", p2.DependsOn)
}

// TestWithSpell verifies that WithSpell registers a tool by name.
func TestWithSpell(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("go"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	p := ws.Get(".")
	require.NotNil(t, p, "project . not found")
	assert.Equal(t, "go", p.Spell)
	require.Len(t, p.Bindings, 1)
	assert.Equal(t, "go", p.Bindings[0].Name)
}

func init() {
	// A spell whose ci op stands in for a provider-driven toolchain's own composed
	// pipeline (e.g. `nx run project:ci`) - the RunCI anchor tests below need a
	// registered spell with a ci op, and a provided project has no magusfile to
	// declare one in. Registered for the WHOLE package (the registry is a process
	// singleton with no scoped teardown), so every test in package magus sees this
	// phantom spell; a test enumerating registered spells must account for it.
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell("ci-capable", spells.WithTargets("ci")))
}

// These tests live in package magus, beside no provider.go, because what they cover
// is the LOAD ORDER in magus.go: that providers run after magusfile evaluation and
// before WorkspaceRegistry.Apply, and that a provided project is indistinguishable
// from a declared one everywhere downstream. The unit-level behaviour of the fold
// itself is tested in internal/workspace/provider_test.go.

// providedProjects is what the fake runner reports for the next Inspect. The
// library's test binary does not link the bindings package, so the real runner is
// never registered and this one owns the hook for the whole test binary. It is
// package state, so these tests must not call t.Parallel.
var providedProjects []spells.ProvidedProject

func init() {
	workspace.RegisterProviderRunner(func(context.Context, string, string) ([]spells.ProvidedProject, error) {
		return providedProjects, nil
	})
}

// withProvided sets the fake runner's answer for one test.
func withProvided(t *testing.T, pp ...spells.ProvidedProject) {
	t.Helper()
	providedProjects = pp
	t.Cleanup(func() { providedProjects = nil })
}

// TestProvidedProjectIsAFullProject is the end-to-end claim the whole mechanism
// rests on: a directory with no magusfile becomes an ordinary project - it is
// listed, it carries the spells and globs the provider reported, and it lands in
// the dependency graph - purely because a provider named it.
func TestProvidedProjectIsAFullProject(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz", "libs/foo/package.json", "libs/shared/package.json")
	withProvided(t,
		spells.ProvidedProject{Path: "libs/shared", Spells: []string{"ts"}},
		spells.ProvidedProject{
			Path:      "libs/foo",
			Name:      "@acme/foo",
			Spells:    []string{"ts"},
			DependsOn: []string{"libs/shared"},
			Outputs:   []string{"dist/**"},
		},
	)
	reg := NewWorkspaceRegistry()
	reg.AddProvider("nx")

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)

	foo := ws.Get("libs/foo")
	require.NotNil(t, foo, "a provided project must be addressable like any other")
	assert.Equal(t, "@acme/foo", foo.Name)
	assert.Equal(t, types.ProvidedBy("nx"), foo.Origin)
	assert.Equal(t, []string{"ts"}, foo.Spells)
	assert.Equal(t, []string{"libs/shared"}, foo.DependsOn)
	assert.Equal(t, []string{"dist/**"}, foo.Outputs)
	assert.Equal(t, filepath.Join(ws.Root(), "libs", "foo"), foo.Dir)

	g, err := ws.Graph()
	require.NoError(t, err)
	require.NotNil(t, g, "provided projects must reach the dependency graph")
}

// TestProviderThenCentralFormComposes pins the ordering the fold depends on: the
// provider runs before WorkspaceRegistry.Apply, so magus\project("libs/foo", {...})
// in the root magusfile layers ONTO a provided project instead of failing with
// ErrUnknownProject or being overwritten by it.
func TestProviderThenCentralFormComposes(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz", "libs/foo/package.json")
	// No spell here on purpose: binding one unions its own globs into Sources, which
	// would obscure the ordering this test exists to pin.
	withProvided(t, spells.ProvidedProject{Path: "libs/foo", Sources: []string{"**/*.ts"}})
	reg := NewWorkspaceRegistry()
	reg.AddProvider("nx")
	reg.RegisterProject("libs/foo", workspace.WithSources("schema/**/*.json"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)

	foo := ws.Get("libs/foo")
	require.NotNil(t, foo)
	assert.Equal(t, []string{"**/*.ts", "schema/**/*.json"}, foo.Sources,
		"the magusfile's sources are layered after the provider's, never instead of them")
}

// TestProviderCannotShadowAMagusfile is the precedence rule stated as a test: a
// directory that declares itself keeps its own definition.
func TestProviderCannotShadowAMagusfile(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz", "libs/foo/magusfile.buzz")
	withProvided(t, spells.ProvidedProject{Path: "libs/foo", Name: "from-provider"})
	reg := NewWorkspaceRegistry()
	reg.AddProvider("nx")

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)

	foo := ws.Get("libs/foo")
	require.NotNil(t, foo)
	assert.Equal(t, types.OriginMagusfile, foo.Origin)
	assert.Empty(t, foo.Name)
}

// TestNoProviderWiredChangesNothing keeps the cost of the mechanism at zero for
// every workspace that does not use one.
func TestNoProviderWiredChangesNothing(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz", "libs/foo/package.json")
	withProvided(t, spells.ProvidedProject{Path: "libs/foo"})

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(NewWorkspaceRegistry()))
	require.NoError(t, err)

	assert.Nil(t, ws.Get("libs/foo"), "no wiring, no fold")
	assert.Len(t, ws.All(), 1)
}

// TestProvidedProjectRejectsEscapingPath asserts the guard survives the real load
// path, not just the unit under it: a bad path fails Inspect rather than producing
// a workspace that silently lacks a project.
func TestProvidedProjectRejectsEscapingPath(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	require.NoError(t, os.MkdirAll(filepath.Join(filepath.Dir(root), "outside"), 0o755))
	withProvided(t, spells.ProvidedProject{Path: "../outside"})
	reg := NewWorkspaceRegistry()
	reg.AddProvider("nx")

	_, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))

	require.Error(t, err)
	assert.Contains(t, err.Error(), string(types.ProviderPathRejected))
}

// TestRunCIAnchorAllowsProvidedProjectWithCIOp verifies the RunCI anchor check
// (anyProjectDeclaresCI) counts a provided project whose bound spell exposes a ci
// op. A provider workspace has no magusfile for ci to live in, so the spell op is
// the only place it can - and `magus run ci` already dispatches to it via the
// ordinary spell fan-out, so RunCI refusing first with MGS1001 would be blocking a
// run that would otherwise succeed.
func TestRunCIAnchorAllowsProvidedProjectWithCIOp(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz", "libs/foo/package.json")
	withProvided(t, spells.ProvidedProject{Path: "libs/foo", Spells: []string{"ci-capable"}})
	reg := NewWorkspaceRegistry()
	reg.AddProvider("nx")

	ctx := context.Background()
	m, err := Open(ctx, root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	defer func() { _ = m.Close() }()

	err = m.RunCI(ctx, []types.Target{{Path: "libs/foo", Name: "ci"}})
	assert.False(t, errors.Is(err, types.NoCITarget),
		"a provided project whose spell has a ci op must satisfy the anchor, got: %v", err)
}

// TestRunCIAnchorRejectsProvidedProjectWithoutCIOp verifies the anchor still
// refuses a provided project when none of its bound spells declare a ci op -
// counting provided projects at all must not make the anchor unconditionally pass.
func TestRunCIAnchorRejectsProvidedProjectWithoutCIOp(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz", "libs/foo/package.json")
	withProvided(t, spells.ProvidedProject{Path: "libs/foo", Spells: []string{"ts"}})
	reg := NewWorkspaceRegistry()
	reg.AddProvider("nx")

	ctx := context.Background()
	m, err := Open(ctx, root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	defer func() { _ = m.Close() }()

	err = m.RunCI(ctx, []types.Target{{Path: "libs/foo", Name: "ci"}})
	assert.True(t, errors.Is(err, types.NoCITarget),
		"a provided project with no ci-declaring spell must still hit the anchor, got: %v", err)
}
