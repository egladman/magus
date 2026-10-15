package magus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeWorkspaceRoot lays down project manifest stubs and returns the
// temp directory path. Does not call Open — lets the caller register
// options first.
func makeWorkspaceRoot(t *testing.T, manifests ...string) string {
	t.Helper()
	w := testkit.NewWorkspace(t)
	for _, rel := range manifests {
		w.Write(rel, "")
	}
	return w.Root()
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
	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("go"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)
	p := ws.Get(".")
	require.NotNil(t, p, "project . not discovered")
	assert.Equal(t, "go", p.Spell) //nolint:fieldwise // an Inspect-built Project carries resolved spells, bindings and discovery state this test cannot construct
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
	assert.Equal(t, "go", p.Spell, "Spell (primary)") //nolint:fieldwise // an Inspect-built Project carries resolved spells, bindings and discovery state this test cannot construct
	assert.Equal(t, []string{"go", "rust"}, p.Spells)
	assert.Len(t, p.ResolvedSpells, 2)
}

// TestWithSpellUnknownTool verifies that WithSpell("nope") errors
// out at Open time rather than silently doing nothing.
func TestWithSpellUnknownTool(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("nope"))

	_, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	assert.ErrorIs(t, err, types.ErrSpellNotRegistered, "Inspect: expected error for unknown tool")
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

// These tests live in package magus, beside no provider.go, because what they cover
// is the LOAD ORDER in magus.go: that providers run after magusfile evaluation and
// before WorkspaceRegistry.Apply, and that a provided project is indistinguishable
// from a declared one everywhere downstream. The unit-level behaviour of the fold
// itself is tested in internal/workspace/provider_test.go.

// provider registers a spell whose list_projects answers pp as a Buzz spell would, and
// returns its name to wire. The engine's own runner invokes it, so these tests cover the
// decode a real provider's answer goes through.
func provider(t *testing.T, pp ...spells.ProvidedProject) string {
	t.Helper()
	list := func(ss []string) []any {
		out := make([]any, 0, len(ss))
		for _, s := range ss {
			out = append(out, s)
		}
		return out
	}
	items := make([]any, 0, len(pp))
	for _, p := range pp {
		items = append(items, map[string]any{
			"path": p.Path, "name": p.Name, "spells": list(p.Spells),
			"depends_on": list(p.DependsOn), "sources": list(p.Sources), "outputs": list(p.Outputs),
		})
	}
	return register.Spell(t, "provider", spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
		if req.Target != spells.ListProjectsContract {
			return nil, nil
		}
		return items, nil
	}))
}

// TestProvidedProjectIsAFullProject is the end-to-end claim the whole mechanism
// rests on: a directory with no magusfile becomes an ordinary project (it is
// listed, it carries the spells and globs the provider reported, and it lands in
// the dependency graph) purely because a provider named it.
func TestProvidedProjectIsAFullProject(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz", "libs/foo/package.json", "libs/shared/package.json")
	ts := register.Spell(t, "ts", spells.WithSources("**/*.ts", "package.json"))
	nx := provider(t,
		spells.ProvidedProject{Path: "libs/shared", Spells: []string{ts}},
		spells.ProvidedProject{
			Path:      "libs/foo",
			Name:      "@acme/foo",
			Spells:    []string{ts},
			DependsOn: []string{"libs/shared"},
			Outputs:   []string{"dist/**"},
		},
	)
	reg := NewWorkspaceRegistry()
	reg.AddProvider(nx)

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)

	foo := ws.Get("libs/foo")
	require.NotNil(t, foo, "a provided project must be addressable like any other")
	assert.Equal(t, "@acme/foo", foo.Name) //nolint:fieldwise // an Inspect-built Project carries resolved spells, bindings and discovery state this test cannot construct
	assert.Equal(t, types.ProvidedBy(nx), foo.Origin)
	assert.Equal(t, []string{ts}, foo.Spells)
	assert.Equal(t, []string{"libs/shared"}, foo.DependsOn)
	assert.Equal(t, []types.Glob{{Pattern: "dist/**"}}, foo.Outputs)
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
	nx := provider(t, spells.ProvidedProject{Path: "libs/foo", Sources: []string{"**/*.ts"}})
	reg := NewWorkspaceRegistry()
	reg.AddProvider(nx)
	reg.RegisterProject("libs/foo", workspace.WithSources("schema/**/*.json"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)

	foo := ws.Get("libs/foo")
	require.NotNil(t, foo)
	assert.Equal(t, []types.Glob{{Pattern: "**/*.ts"}, {Pattern: "schema/**/*.json"}}, foo.Sources,
		"the magusfile's sources are layered after the provider's, never instead of them")
}

// TestProviderCannotShadowAMagusfile is the precedence rule stated as a test: a
// directory that declares itself keeps its own definition.
func TestProviderCannotShadowAMagusfile(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz", "libs/foo/magusfile.buzz")
	nx := provider(t, spells.ProvidedProject{Path: "libs/foo", Name: "from-provider"})
	reg := NewWorkspaceRegistry()
	reg.AddProvider(nx)

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
	provider(t, spells.ProvidedProject{Path: "libs/foo"})

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
	nx := provider(t, spells.ProvidedProject{Path: "../outside"})
	reg := NewWorkspaceRegistry()
	reg.AddProvider(nx)

	_, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))

	require.Error(t, err)
	assert.Contains(t, err.Error(), string(types.ProviderPathRejected))
}

// TestRunCIAnchorAllowsProvidedProjectWithCIOp verifies the RunCI anchor check
// (anyProjectDeclaresCI) counts a provided project whose bound spell exposes a ci
// op. A provider workspace has no magusfile for ci to live in, so the spell op is
// the only place it can, and `magus run ci` already dispatches to it via the
// ordinary spell fan-out, so RunCI refusing first with MGS1001 would be blocking a
// run that would otherwise succeed.
func TestRunCIAnchorAllowsProvidedProjectWithCIOp(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz", "libs/foo/package.json")
	// The ci op stands in for a provider-driven toolchain's own composed pipeline (e.g.
	// `nx run project:ci`): a provided project has no magusfile to declare one in.
	ci := register.Spell(t, "ci-capable", spells.WithTargets("ci"))
	nx := provider(t, spells.ProvidedProject{Path: "libs/foo", Spells: []string{ci}})
	reg := NewWorkspaceRegistry()
	reg.AddProvider(nx)

	ctx := context.Background()
	m, err := Open(ctx, root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	defer func() { _ = m.Close() }()

	err = m.RunCI(ctx, []types.Target{{Path: "libs/foo", Name: "ci"}})
	assert.False(t, errors.Is(err, types.NoCITarget),
		"a provided project whose spell has a ci op must satisfy the anchor, got: %v", err)
}

// TestRunCIAnchorRejectsProvidedProjectWithoutCIOp verifies the anchor still
// refuses a provided project when none of its bound spells declare a ci op:
// counting provided projects at all must not make the anchor unconditionally pass.
func TestRunCIAnchorRejectsProvidedProjectWithoutCIOp(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz", "libs/foo/package.json")
	ts := register.Spell(t, "ts", spells.WithSources("**/*.ts", "package.json"))
	nx := provider(t, spells.ProvidedProject{Path: "libs/foo", Spells: []string{ts}})
	reg := NewWorkspaceRegistry()
	reg.AddProvider(nx)

	ctx := context.Background()
	m, err := Open(ctx, root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	defer func() { _ = m.Close() }()

	err = m.RunCI(ctx, []types.Target{{Path: "libs/foo", Name: "ci"}})
	assert.True(t, errors.Is(err, types.NoCITarget),
		"a provided project with no ci-declaring spell must still hit the anchor, got: %v", err)
}

func TestIgnorePatternConstructors(t *testing.T) {
	assert.Equal(t, types.IgnorePattern{Type: types.PatternGlob, Pattern: "**/*.tmp"}, IgnoreGlob("**/*.tmp"))
	assert.Equal(t, types.IgnorePattern{Type: types.PatternRegex, Pattern: `\.swp$`}, IgnoreRegex(`\.swp$`))
	assert.Equal(t, types.IgnorePattern{Type: types.PatternLiteral, Pattern: "node_modules"}, IgnoreLiteral("node_modules"))
}

// TestWithWatchIgnoreReachesTheProject: the patterns are appended at registration
// time, so a workspace that opened successfully has already validated them.
func TestWithWatchIgnoreReachesTheProject(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
	})

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("api", WithWatchIgnore(IgnoreGlob("**/*.tmp"), IgnoreLiteral("node_modules")))

	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	api := m.Get("api")
	require.NotNil(t, api)
	assert.Subset(t, api.WatchIgnores, []types.IgnorePattern{
		{Type: types.PatternGlob, Pattern: "**/*.tmp"},
		{Type: types.PatternLiteral, Pattern: "node_modules"},
	})
}

// TestWithWatchIgnoreRejectsAMalformedRegexAtOpen: a pattern that cannot compile
// would silently match nothing, so it fails the load instead.
func TestWithWatchIgnoreRejectsAMalformedRegexAtOpen(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
	})

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("api", WithWatchIgnore(IgnoreRegex("(unclosed")))

	_, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	assert.Error(t, err, "an ignore pattern that cannot compile must not open the workspace")
}

// TestWorkspaceRegistryRidesTheContext is how an interpreter recovers the registry
// its Open was given: there is no global, so a bare context yields nil rather than
// a default nobody registered into.
func TestWorkspaceRegistryRidesTheContext(t *testing.T) {
	assert.Nil(t, WorkspaceRegistryFromContext(context.Background()))

	reg := NewWorkspaceRegistry()
	ctx := WithWorkspaceRegistryContext(context.Background(), reg)
	assert.Same(t, reg.r, WorkspaceRegistryFromContext(ctx).r)
}

// TestWithoutWorkspaceProvidersLeavesMagusfileProjectsAlone: the option suppresses
// magus\workspace.provider, which a tree with none declared cannot show. What it
// must NOT do is change the declared inventory.
func TestWithoutWorkspaceProvidersLeavesMagusfileProjectsAlone(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
	})

	ws, err := Inspect(context.Background(), root, WithoutWorkspaceProviders())
	require.NoError(t, err)

	paths := make([]string, 0, len(ws.All()))
	for _, p := range ws.All() {
		paths = append(paths, p.Path)
	}
	assert.ElementsMatch(t, []string{".", "api"}, paths)
}

// TestOpenRefusesABrokerItWouldNotUse pins that no pair of broker options is silently
// contradictory: a client beside a resolved policy of off, from the option or from the
// workspace's own setting, fails Open naming where the off came from, and so does a nil
// client. A client beside a policy that uses it opens. Nothing here dials: the client
// connects on the first claim, and none is taken.
func TestOpenRefusesABrokerItWouldNotUse(t *testing.T) {
	client := broker.NewClient("unix:///nonexistent/broker.sock")
	cases := map[string]struct {
		yaml    string
		opts    []Option
		wantErr string
	}{
		"off by option": {
			opts:    []Option{WithBroker(client), WithBrokerPolicy(types.BrokerOff)},
			wantErr: "magus: WithBroker passed a client, but WithBrokerPolicy is off, so it would never be used, drop one or the other",
		},
		"off by the workspace setting": {
			yaml:    "broker: off\n",
			opts:    []Option{WithBroker(client)},
			wantErr: "magus: WithBroker passed a client, but the workspace's broker setting is off, so it would never be used, drop one or the other",
		},
		"a nil client": {
			opts:    []Option{WithBroker(nil), WithBrokerPolicy(types.BrokerBestEffort)},
			wantErr: "magus: WithBroker was given a nil client",
		},
		"a client under best-effort": {
			opts: []Option{WithBroker(client), WithBrokerPolicy(types.BrokerBestEffort)},
		},
		"the option overrides the setting": {
			yaml: "broker: off\n",
			opts: []Option{WithBroker(client), WithBrokerPolicy(types.BrokerRequired)},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{"magusfile.buzz": ""}
			if tc.yaml != "" {
				files["magus.yaml"] = tc.yaml
			}
			m, err := Open(t.Context(), writeWorkspace(t, files), tc.opts...)
			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.NoError(t, m.Close())
		})
	}
}

// The push gate needs a passing `ci`, so a human_only target anywhere in its chain makes
// every agent push unclearable; the load refuses it rather than the push.
func TestRefuseHumanOnlyInGate(t *testing.T) {
	bench := types.Target{HumanOnly: true, HumanOnlyReason: "launches paid sessions"}
	proj := func(path string, chains map[string][]types.ChainStep, policies map[string]types.Target) *types.Project {
		return &types.Project{Path: path, TargetChains: chains, TargetPolicies: policies}
	}
	benchPolicy := map[string]types.Target{"bench": bench}
	gateOnBench := map[string][]types.ChainStep{"ci": {{Target: "test"}}, "test": {{Target: "bench"}}}
	gateOnTest := map[string][]types.ChainStep{"ci": {{Target: "test"}}}
	gateOnEvals := map[string][]types.ChainStep{"ci": {{Project: "evals", Target: "grade"}}}
	cases := map[string]struct {
		projects []*types.Project
		wantErr  []string
	}{
		"ci itself": {
			projects: []*types.Project{proj(".", nil, map[string]types.Target{"ci": bench})},
			wantErr:  []string{"project \".\" declares its `ci` human_only"},
		},
		"a member ci reaches": {
			projects: []*types.Project{proj(".", gateOnBench, benchPolicy)},
			wantErr:  []string{"`ci` in project \".\" reaches human_only target .:bench", "run `magus run bench .` as its own CI step"},
		},
		"a member in another project": {
			projects: []*types.Project{proj(".", gateOnEvals, nil), proj("evals", nil, map[string]types.Target{"grade": bench})},
			wantErr:  []string{"`ci` in project \".\" reaches human_only target evals:grade"},
		},
		"a target outside the gate": {
			projects: []*types.Project{proj(".", gateOnTest, benchPolicy)},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lookup := func(path string) *types.Project {
				for _, p := range tc.projects {
					if p.Path == path {
						return p
					}
				}
				return nil
			}
			err := refuseHumanOnlyInGate(tc.projects, lookup)
			if len(tc.wantErr) == 0 {
				require.NoError(t, err)
				return
			}
			for _, want := range tc.wantErr {
				assert.ErrorContains(t, err, want)
			}
		})
	}
}
