package magus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	p := ws.Get("extensions/drape")
	if p == nil {
		t.Fatal("project extensions/drape not found")
	}
	want := "extensions/api"
	found := false
	for _, dep := range p.DependsOn {
		if dep == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Deps = %v, want %q in the list", p.DependsOn, want)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	p := ws.Get("a/b/c")
	if p == nil {
		t.Fatal("project a/b/c not found")
	}
	want := "."
	found := false
	for _, dep := range p.DependsOn {
		if dep == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Deps = %v, want %q in the list", p.DependsOn, want)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	p := ws.Get(".")
	if p == nil {
		t.Fatal("project . not found")
	}
	want := "api"
	found := false
	for _, dep := range p.DependsOn {
		if dep == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Deps = %v, want %q in the list", p.DependsOn, want)
	}
}

// TestWithDependsOnEscapesRoot verifies that a relative path that
// would escape the workspace root is rejected.
func TestWithDependsOnEscapesRoot(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithDependsOn("../outside"))

	_, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	if err == nil {
		t.Fatal("Inspect: expected error for path escaping workspace root")
	}
}

// TestWithSpellAddsLanguage verifies that WithSpell(name) populates
// both the Spell and Spells fields via Register.
func TestWithSpellAddsLanguage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("//go:build magus\npackage main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("go"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	if err != nil {
		t.Fatal(err)
	}
	p := ws.Get(".")
	if p == nil {
		t.Fatal("project . not discovered")
	}
	if p.Spell != "go" {
		t.Errorf("Spell = %q, want %q", p.Spell, "go")
	}
	if len(p.Spells) != 1 || p.Spells[0] != "go" {
		t.Errorf("Spells = %v, want [\"go\"]", p.Spells)
	}
}

// TestWithSpellMultipleTools verifies that calling WithSpell twice registers
// two tools and that both appear in p.Spells in registration order.
func TestWithSpellMultipleTools(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("go"), WithSpell("rust"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p := ws.Get(".")
	if p == nil {
		t.Fatal("project . not found")
	}
	if p.Spell != "go" {
		t.Errorf("Spell (primary) = %q, want \"go\"", p.Spell)
	}
	if len(p.Spells) != 2 || p.Spells[0] != "go" || p.Spells[1] != "rust" {
		t.Errorf("Spells = %v, want [\"go\" \"rust\"]", p.Spells)
	}
	if spells := p.ResolvedSpells; len(spells) != 2 {
		t.Errorf("ResolvedSpells() returned %d spells, want 2", len(spells))
	}
}

// TestWithSpellUnknownTool verifies that WithSpell("nope") errors
// out at Open time rather than silently doing nothing.
func TestWithSpellUnknownTool(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("//go:build magus\npackage main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("nope"))

	_, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	if err == nil {
		t.Fatal("Inspect: expected error for unknown tool")
	}
	if !errors.Is(err, ErrSpellNotRegistered) {
		t.Fatalf("error %q is not ErrSpellNotRegistered", err)
	}
}

// TestWithExclusiveOption verifies that WithExclusive() sets p.Exclusive.
func TestWithExclusiveOption(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithExclusive())

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p := ws.Get(".")
	if p == nil {
		t.Fatal("project . not found")
	}
	if !p.Exclusive {
		t.Error("Exclusive = false, want true")
	}
}

// TestWithClaimExtendsClaims verifies that WithClaim adds globs to the
// pack's Binding.AddedClaims.
func TestWithClaimExtendsClaims(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("go", WithClaim("**/*.proto", "**/*.thrift")))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p := ws.Get(".")
	if p == nil {
		t.Fatal("project . not found")
	}
	if len(p.Bindings) != 1 {
		t.Fatalf("Bindings len = %d; want 1", len(p.Bindings))
	}
	got := p.Bindings[0].AddedClaims
	want := []string{"**/*.proto", "**/*.thrift"}
	if len(got) != len(want) {
		t.Fatalf("AddedClaims = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AddedClaims[%d] = %q; want %q", i, got[i], want[i])
		}
	}
}

// TestWithoutClaimOnBinding verifies that WithoutClaim inside
// WithSpell populates the pack's Binding.RemovedClaims.
func TestWithoutClaimOnBinding(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("ts", WithoutClaim("**/*.json")))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p := ws.Get(".")
	if p == nil {
		t.Fatal("project . not found")
	}
	if len(p.Bindings) != 1 {
		t.Fatalf("Bindings len = %d; want 1", len(p.Bindings))
	}
	if p.Bindings[0].Name != "ts" {
		t.Errorf("Bindings[0].Name = %q; want %q", p.Bindings[0].Name, "ts")
	}
	if len(p.Bindings[0].RemovedClaims) != 1 || p.Bindings[0].RemovedClaims[0] != "**/*.json" {
		t.Errorf("RemovedClaims = %v; want [\"**/*.json\"]", p.Bindings[0].RemovedClaims)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	p := ws.Get(".")
	if p == nil {
		t.Fatal("project . not found")
	}

	countDep := func(deps []string, target string) int {
		n := 0
		for _, d := range deps {
			if d == target {
				n++
			}
		}
		return n
	}

	if n := countDep(p.DependsOn, "api"); n != 1 {
		t.Fatalf("after first Open: Deps has %d copies of %q, want 1; Deps = %v", n, "api", p.DependsOn)
	}

	// A second Inspect with the same registry must not double the deps;
	// each *Workspace is distinct.
	ws2, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	p2 := ws2.Get(".")
	if p2 == nil {
		t.Fatal("project . not found in second workspace")
	}
	if n := countDep(p2.DependsOn, "api"); n != 1 {
		t.Fatalf("after second Open: Deps has %d copies of %q, want 1; Deps = %v", n, "api", p2.DependsOn)
	}
}

// TestWithSpell verifies that WithSpell registers a tool by name.
func TestWithSpell(t *testing.T) {
	root := makeWorkspaceRoot(t, "magusfile.buzz")

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("go"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p := ws.Get(".")
	if p == nil {
		t.Fatal("project . not found")
	}
	if got, want := p.Spell, "go"; got != want {
		t.Errorf("Spell = %q; want %q", got, want)
	}
	if len(p.Bindings) != 1 || p.Bindings[0].Name != "go" {
		t.Errorf("Bindings = %+v; want one binding for %q", p.Bindings, "go")
	}
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
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p := ws.Get(".")
	if p == nil {
		t.Fatal("project . not found")
	}
	if len(p.Bindings) != 2 {
		t.Fatalf("Bindings len = %d; want 2", len(p.Bindings))
	}
	if got, want := p.Bindings[0].ClaimWeight, 10; got != want {
		t.Errorf("Bindings[0].ClaimWeight = %d; want %d", got, want)
	}
	if got, want := p.Bindings[1].ClaimWeight, 0; got != want {
		t.Errorf("Bindings[1].ClaimWeight = %d; want %d", got, want)
	}
}
