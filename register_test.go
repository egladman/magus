package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/project"
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
		claims  []string
		sources []string
	}{
		{"go", nil, []string{"**/*.go", "go.mod", "go.sum"}},
		{"rust", nil, []string{"**/*.rs", "Cargo.toml"}},
		{"ts", []string{"**/*.ts", "**/*.tsx"}, []string{"**/*.ts", "**/*.tsx", "package.json"}},
		{"json", []string{"**/*.json"}, []string{"**/*.json", "**/*.jsonc"}},
	} {
		m := meta
		project.DefaultSpellRegistry().RegisterSpell(types.NewSpell(
			m.name,
			types.WithSources(m.sources...),
			types.WithClaims(m.claims...),
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
	assert.ErrorIs(t, err, ErrSpellNotRegistered, "Inspect: expected error for unknown tool")
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

// TestWithClaimExtendsClaims verifies that WithClaim adds globs to the
// pack's Binding.AddedClaims.
func TestWithClaimExtendsClaims(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("go", WithClaim("**/*.proto", "**/*.thrift")))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	p := ws.Get(".")
	require.NotNil(t, p, "project . not found")
	require.Len(t, p.Bindings, 1)
	assert.Equal(t, []string{"**/*.proto", "**/*.thrift"}, p.Bindings[0].AddedClaims)
}

// TestWithoutClaimOnBinding verifies that WithoutClaim inside
// WithSpell populates the pack's Binding.RemovedClaims.
func TestWithoutClaimOnBinding(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("ts", WithoutClaim("**/*.json")))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	p := ws.Get(".")
	require.NotNil(t, p, "project . not found")
	require.Len(t, p.Bindings, 1)
	assert.Equal(t, "ts", p.Bindings[0].Name)
	assert.Equal(t, []string{"**/*.json"}, p.Bindings[0].RemovedClaims)
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

// TestWithClaimWeight verifies that WithClaimWeight sets Binding.ClaimWeight.
func TestWithClaimWeight(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(
		".",
		WithSpell("ts", WithClaimWeight(10)),
		WithSpell("go"),
	)

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	p := ws.Get(".")
	require.NotNil(t, p, "project . not found")
	require.Len(t, p.Bindings, 2)
	assert.Equal(t, 10, p.Bindings[0].ClaimWeight)
	assert.Equal(t, 0, p.Bindings[1].ClaimWeight)
}
