package magus

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
)

// TestContainsAll covers the StreamAllSentinel detection used by the
// affected --stdin streaming flow.
func TestContainsAll(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		paths []string
		want  bool
	}{
		{"empty slice", nil, false},
		{"only sentinel", []string{StreamAllSentinel}, true},
		{"sentinel mid", []string{"a", StreamAllSentinel, "b"}, true},
		{"no sentinel", []string{"a", "b"}, false},
		{"sentinel-like prefix", []string{"--all-things"}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := slices.Contains(tc.paths, StreamAllSentinel); got != tc.want {
				t.Errorf("slices.Contains(%v, StreamAllSentinel) = %v, want %v", tc.paths, got, tc.want)
			}
		})
	}
}

// TestMakeHandlerConventionalTargets asserts that makeHandler returns a
// non-nil handler for the seven conventional target names and that invoking
// it on an empty project (no packs) returns nil without panic.
func TestMakeHandlerConventionalTargets(t *testing.T) {
	m := &Magus{}
	p := &types.Project{} // no packs → every handler must return nil cleanly
	ctx := context.Background()
	for _, name := range []string{"preflight", "build", "test", "lint", "format", "clean", "generate"} {
		h := m.makeHandler(name)
		if h == nil {
			t.Errorf("makeHandler(%q) = nil; expected non-nil handler", name)
			continue
		}
		if err := h(ctx, p); err != nil {
			t.Errorf("makeHandler(%q)(ctx, emptyProject) = %v; want nil", name, err)
		}
	}
	// Arbitrary non-conventional names also get a handler.
	h := m.makeHandler("go-build")
	if h == nil {
		t.Errorf("makeHandler(%q) = nil; any target name should produce a handler", "go-build")
	} else if err := h(ctx, p); err != nil {
		t.Errorf("makeHandler(%q)(ctx, emptyProject) = %v; want nil", "go-build", err)
	}
}

// TestSpellTargetSources asserts that TargetSources returns the correct globs for a
// known target and nil for unknown targets, covering the generic enrichment loop in
// executeOnProjects.
func TestSpellTargetSources(t *testing.T) {
	t.Parallel()
	vs := map[string][]string{
		"lint": {".golangci.yml", ".golangci.yaml", ".golangci.toml", ".golangci.json"},
	}
	s := types.NewSpell("testpack", types.WithTargetSources(vs))

	cases := []struct {
		target string
		want   []string
	}{
		{"lint", []string{".golangci.yml", ".golangci.yaml", ".golangci.toml", ".golangci.json"}},
		{"build", nil},
		{"", nil},
	}
	for _, tc := range cases {
		got := s.TargetSources()[tc.target]
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("TargetSources(%q) = %v, want %v", tc.target, got, tc.want)
		}
	}
}

// TestSpellTargetSources_nilMap asserts that TargetSources is nil-safe on a
// Spell constructed without any verb_sources (the common case for non-Go spells).
func TestSpellTargetSources_nilMap(t *testing.T) {
	t.Parallel()
	s := types.NewSpell("bare")
	if got := s.TargetSources()["lint"]; got != nil {
		t.Errorf("TargetSources on nil-map spell = %v, want nil", got)
	}
}

// TestMergePaths verifies dedup + lex ordering, the contract callers
// rely on for deterministic stream-driven affected output.
func TestMergePaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b []string
		want []string
	}{
		{"both empty", nil, nil, []string{}},
		{"a empty", nil, []string{"x", "a"}, []string{"a", "x"}},
		{"b empty", []string{"x", "a"}, nil, []string{"a", "x"}},
		{"dedup across", []string{"a", "b"}, []string{"b", "c"}, []string{"a", "b", "c"}},
		{"all dupes", []string{"a", "a"}, []string{"a"}, []string{"a"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mergePaths(tc.a, tc.b)
			if got == nil {
				got = []string{}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mergePaths(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestForEachSpell_BudgetHonored verifies that when the context carries a
// cache.Limiter (as set by cache.RunAll), forEachSpell does not exceed the
// configured concurrency cap.  Before the fix, spell goroutines acquired no
// limiter slot, so N projects × M spells could run N×M concurrent subprocesses
// against a cap of N.
func TestForEachSpell_BudgetHonored(t *testing.T) {
	t.Parallel()
	const cap = 2 // concurrency budget

	lim := cache.NewLimiter(cap)

	// Simulate a RunAll worker: the test goroutine has acquired one slot and
	// marked the context accordingly, exactly as cache.RunAll does at
	// cache.go:447-456.
	ctx := context.Background()
	if err := lim.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	defer lim.Release()
	ctx = cache.ContextWithLimiter(ctx, lim)
	ctx = cache.WithSlotHeld(ctx)

	var (
		mu       sync.Mutex
		inFlight int
		peak     int
	)

	newSpell := func(name string) *types.Spell {
		return types.NewSpell(name, types.WithInvoker(func(_ context.Context, _ types.InvokeRequest) (any, error) {
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			mu.Unlock()

			time.Sleep(20 * time.Millisecond) // hold long enough for overlap to be detectable

			mu.Lock()
			inFlight--
			mu.Unlock()
			return nil, nil
		}))
	}

	p := &types.Project{
		Path: "test-budget",
		ResolvedSpells: []*types.Spell{
			newSpell("a"), newSpell("b"), newSpell("c"), newSpell("d"),
		},
	}

	if err := forEachSpell(ctx, p, "build", func(ctx context.Context, s *types.Spell) error {
		_, err := s.Invoke(ctx, types.InvokeRequest{Target: "build"})
		return err
	}); err != nil {
		t.Fatalf("forEachSpell: %v", err)
	}

	if peak == 0 {
		t.Fatal("no spells ran")
	}
	if peak > cap {
		t.Errorf("peak concurrent spells = %d; want ≤ %d (limiter cap = %d): spell fan-out escapes the concurrency budget",
			peak, cap, cap)
	}
}

// TestForEachSpell_NoBudgetUnchanged verifies that when no limiter is in the
// context, forEachSpell runs all spells concurrently as before (no regression).
func TestForEachSpell_NoBudgetUnchanged(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		ran []string
	)

	newSpell := func(name string) *types.Spell {
		return types.NewSpell(name, types.WithInvoker(func(_ context.Context, _ types.InvokeRequest) (any, error) {
			mu.Lock()
			ran = append(ran, name)
			mu.Unlock()
			return nil, nil
		}))
	}

	p := &types.Project{
		Path: "test-no-budget",
		ResolvedSpells: []*types.Spell{
			newSpell("x"), newSpell("y"), newSpell("z"),
		},
	}

	if err := forEachSpell(context.Background(), p, "build", func(ctx context.Context, s *types.Spell) error {
		_, err := s.Invoke(ctx, types.InvokeRequest{Target: "build"})
		return err
	}); err != nil {
		t.Fatalf("forEachSpell: %v", err)
	}

	if len(ran) != 3 {
		t.Errorf("ran %d spells, want 3", len(ran))
	}
}

// TestRunTarget_InjectsEffectiveClaims verifies that runTarget injects
// effective claims into the spell context for every target, not just "build".
func TestRunTarget_InjectsEffectiveClaims(t *testing.T) {
	t.Parallel()

	var gotClaims []string

	spell := types.NewSpell(
		"s",
		types.WithClaims("**/*.go"),
		types.WithInvoker(func(ctx context.Context, _ types.InvokeRequest) (any, error) {
			gotClaims = types.EffectiveClaimsFromContext(ctx)
			return nil, nil
		}),
	)

	p := &types.Project{
		Path:           "p",
		Spells:         []string{"s"},
		Bindings:       []*types.Binding{{Name: "s"}},
		ResolvedSpells: []*types.Spell{spell},
	}

	if err := runTarget(context.Background(), p, "ci"); err != nil {
		t.Fatal(err)
	}
	if len(gotClaims) == 0 {
		t.Error("runTarget did not inject effective claims into the spell context")
	}
}

// TestCacheOps_InspectReturnErrNoCache asserts the cache-operation methods
// report ErrNoCache on an Inspect-constructed (cache-free) workspace rather
// than panicking on a nil cache.
func TestCacheOps_InspectReturnErrNoCache(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	m, err := inspect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.PruneCache(context.Background(), time.Now(), true); !errors.Is(err, ErrNoCache) {
		t.Errorf("PruneCache on Inspect = %v, want ErrNoCache", err)
	}
	if err := m.ExportCache(context.Background(), io.Discard); !errors.Is(err, ErrNoCache) {
		t.Errorf("ExportCache on Inspect = %v, want ErrNoCache", err)
	}
	if err := m.ImportCache(context.Background(), strings.NewReader("")); !errors.Is(err, ErrNoCache) {
		t.Errorf("ImportCache on Inspect = %v, want ErrNoCache", err)
	}
	if _, err := m.TailLog("anything", ""); !errors.Is(err, ErrNoCache) {
		t.Errorf("TailLog on Inspect = %v, want ErrNoCache", err)
	}
}

func TestLimiter_IdempotentNonNil(t *testing.T) {
	t.Parallel()
	m := &Magus{}
	l1 := m.limiter()
	if l1 == nil {
		t.Fatal("limiter() = nil, want non-nil")
	}
	if l2 := m.limiter(); l1 != l2 {
		t.Error("limiter() not idempotent: second call returned different pointer")
	}
}

func TestPoolRegistry_IdempotentNonNil(t *testing.T) {
	t.Parallel()
	m := &Magus{}
	r1 := m.buzzPoolRegistry()
	if r1 == nil {
		t.Fatal("buzzPoolRegistry() = nil, want non-nil")
	}
	if r2 := m.buzzPoolRegistry(); r1 != r2 {
		t.Error("buzzPoolRegistry() not idempotent: second call returned different pointer")
	}
}

func TestClose_Idempotent(t *testing.T) {
	t.Parallel()
	m := &Magus{}
	if err := m.Close(); err != nil {
		t.Errorf("Close on fresh Magus: %v", err)
	}
	// Trigger lazy init of poolReg, then Close again.
	_ = m.buzzPoolRegistry()
	if err := m.Close(); err != nil {
		t.Errorf("Close after PoolRegistry: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestSetGraphObserver_Invoked(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	m, err := inspect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}

	var builds int
	m.SetGraphObserver(&countingObserver{onBuild: func() { builds++ }})
	if _, err := m.Graph(); err != nil {
		t.Fatal(err)
	}
	if builds == 0 {
		t.Error("SetGraphObserver: OnBuild never called after Graph()")
	}

	// Clear the observer; subsequent Graph() must not increment.
	before := builds
	m.SetGraphObserver(nil)
	if _, err := m.Graph(); err != nil {
		t.Fatal(err)
	}
	if builds != before {
		t.Errorf("SetGraphObserver(nil): OnBuild called %d extra time(s) after clearing observer", builds-before)
	}
}

func TestSpecFor_RootProject(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz")
	m, err := inspect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	p := &types.Project{
		Path:    ".",
		Sources: []string{"**/*.go"},
		Outputs: []string{"bin/app"},
	}
	spec := m.baseSpec(p)
	if spec.ProjectPath != "." {
		t.Errorf("ProjectPath = %q, want %q", spec.ProjectPath, ".")
	}
	// Root project: declared glob passes through unchanged; magusfile globs are
	// also appended (see magusfileGlobs). Use Contains rather than exact-count.
	if !slices.Contains(spec.Sources, "**/*.go") {
		t.Errorf("Sources = %v, must contain \"**/*.go\"", spec.Sources)
	}
	if !slices.Contains(spec.Sources, "magusfile.buzz") {
		t.Errorf("Sources = %v, must contain root magusfile glob \"magusfile.buzz\"", spec.Sources)
	}
	if len(spec.Outputs) != 1 || spec.Outputs[0] != "bin/app" {
		t.Errorf("Outputs = %v, want [\"bin/app\"]", spec.Outputs)
	}
	if spec.WorkspaceRoot != m.Root() {
		t.Errorf("WorkspaceRoot = %q, want %q", spec.WorkspaceRoot, m.Root())
	}
}

func TestSpecFor_NestedProject(t *testing.T) {
	t.Parallel()
	root := makeWorkspaceRoot(t, "magusfile.buzz", "api/magusfile.buzz")
	m, err := inspect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	p := &types.Project{
		Path:    "api",
		Sources: []string{"**/*.go"},
		Outputs: []string{"bin/server"},
	}
	spec := m.baseSpec(p)
	// Declared glob is prefixed with the project path.
	if !slices.Contains(spec.Sources, "api/**/*.go") {
		t.Errorf("Sources = %v, must contain \"api/**/*.go\"", spec.Sources)
	}
	// Project-local magusfile glob is included.
	if !slices.Contains(spec.Sources, "api/magusfile.buzz") {
		t.Errorf("Sources = %v, must contain \"api/magusfile.buzz\"", spec.Sources)
	}
	// Root magusfile glob is always included for non-root projects.
	if !slices.Contains(spec.Sources, "magusfile.buzz") {
		t.Errorf("Sources = %v, must contain root \"magusfile.buzz\"", spec.Sources)
	}
	if len(spec.Outputs) != 1 || spec.Outputs[0] != "api/bin/server" {
		t.Errorf("Outputs = %v, want [\"api/bin/server\"]", spec.Outputs)
	}
}

// countingObserver is a types.Observer stub that counts OnBuild calls.
type countingObserver struct{ onBuild func() }

func (c *countingObserver) OnBuild(types.BuildStats) {
	if c.onBuild != nil {
		c.onBuild()
	}
}
func (c *countingObserver) OnQuery(types.QueryEvent) {}
func (c *countingObserver) OnError(error)            {}

// TestMagusfileGlobs verifies the workspace-relative glob sets returned
// for root and non-root project paths.
func TestMagusfileGlobs(t *testing.T) {
	t.Parallel()

	t.Run("root project", func(t *testing.T) {
		t.Parallel()
		got := magusfileGlobs(".")
		want := []string{
			"magusfile.buzz",
			"magusfiles/**/*.buzz",
		}
		if !slices.Equal(got, want) {
			t.Errorf("magusfileGlobs(\".\") =\n  %v\nwant\n  %v", got, want)
		}
	})

	t.Run("non-root project", func(t *testing.T) {
		t.Parallel()
		got := magusfileGlobs("extensions/drape")
		want := []string{
			"extensions/drape/magusfile.buzz",
			"extensions/drape/magusfiles/**/*.buzz",
		}
		if !slices.Equal(got, want) {
			t.Errorf("magusfileGlobs(\"extensions/drape\") =\n  %v\nwant\n  %v", got, want)
		}
	})

	t.Run("single-segment path", func(t *testing.T) {
		t.Parallel()
		got := magusfileGlobs("api")
		if len(got) == 0 {
			t.Fatal("expected non-empty globs for single-segment path")
		}
		for _, g := range got {
			if len(g) < 4 || g[:4] != "api/" {
				t.Errorf("glob %q should start with \"api/\"", g)
			}
		}
	})
}

// ExampleWorkspaceRegistry_withSpell shows the recommended way to attach a spell to a
// project using the string-name API. The registry is passed to Inspect or Open
// via WithWorkspaceRegistry.
func ExampleWorkspaceRegistry_withSpell() {
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(
		"api",
		WithSpell("go"),
	)
	// pass reg to Inspect or Open:
	// Inspect(ctx, root, WithWorkspaceRegistry(reg))
}

// ExampleInspect shows how to discover projects in a workspace without
// opening the cache. Inspect is the right entry point for read-only
// commands (list, graph, describe) where cache overhead is unnecessary.
func ExampleInspect() {
	// Create a minimal workspace with one project for illustration.
	root, err := os.MkdirTemp("", "magus-example-*")
	if err != nil {
		fmt.Println("setup error:", err)
		return
	}
	defer os.RemoveAll(root)

	// A directory is a project if it contains a magusfile.buzz.
	projDir := filepath.Join(root, "myapp")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		fmt.Println("setup error:", err)
		return
	}
	if err := os.WriteFile(filepath.Join(projDir, "magusfile.buzz"), []byte(""), 0o644); err != nil {
		fmt.Println("setup error:", err)
		return
	}

	ws, err := Inspect(context.Background(), root)
	if err != nil {
		fmt.Println("inspect error:", err)
		return
	}

	for _, p := range ws.All() {
		fmt.Println(p.Path)
	}
	// Output:
	// myapp
}

// ExampleOpen shows the canonical entry point: open a Magus
// orchestrator rooted at "." and run a target across every project.
func ExampleOpen() {
	m, err := Open(context.Background(), ".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	targets, err := m.ExpandPath(types.Target{Name: "build"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	if err := m.Run(context.Background(), targets); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

// ExampleMagus_ExpandAffected shows how to compute the VCS-diff
// affected project set, with automatic fallback to all projects when
// the VCS command is unavailable (shallow clone, missing binary, etc.).
func ExampleMagus_ExpandAffected() {
	m, err := Open(context.Background(), ".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	targets, source, err := m.ExpandAffected(context.Background(), "test", "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Printf("[%s]\n", source)
	for _, t := range targets {
		fmt.Println(" ", t.Path)
	}
}

// newWorkspace lays down a minimal multi-language workspace and
// returns it as a types.WorkspaceRepository rooted at it.
// Uses Inspect (no cache, no Teal preload) so tests don't need a
// runtime backend registered.
func newWorkspace(t *testing.T) types.WorkspaceRepository {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{
		"magusfile.buzz",                    // root project "."
		"api/magusfile.buzz",                // "api"
		"web/studio/magusfile.buzz",         // "web/studio"
		"extensions/drape/magusfile.buzz",   // "extensions/drape"
		"extensions/lattice/magusfile.buzz", // "extensions/lattice"
	} {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := Inspect(context.Background(), root)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	return ws
}

// TestExpand_AllNoPath fans out to every project when Path is empty.
func TestExpand_AllNoPath(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	targets, err := ws.ExpandPath(types.Target{Name: "build"})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(targets))
	for i, tgt := range targets {
		got[i] = tgt.Path
	}
	slices.Sort(got)
	want := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	if !slices.Equal(got, want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
	for _, tgt := range targets {
		if tgt.Name != "build" {
			t.Fatalf("Target = %q, want %q", tgt.Name, "build")
		}
	}
}

// TestExpand_ExplicitPath returns exactly the requested project.
func TestExpand_ExplicitPath(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	targets, err := ws.ExpandPath(types.Target{Path: "api", Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("len = %d, want 1", len(targets))
	}
	if targets[0].Path != "api" || targets[0].Name != "test" {
		t.Fatalf("target = %+v, want {api test}", targets[0])
	}
}

// TestExpand_SlashAlias verifies "/" is treated as all-projects.
func TestExpand_SlashAlias(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	targets, err := ws.ExpandPath(types.Target{Path: "/", Name: "build"})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(targets))
	for i, tgt := range targets {
		got[i] = tgt.Path
	}
	slices.Sort(got)
	want := []string{".", "api", "extensions/drape", "extensions/lattice", "web/studio"}
	if !slices.Equal(got, want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
}

// TestExpand_WsPrefixRejected verifies "ws:foo" paths are rejected.
func TestExpand_WsPrefixRejected(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	_, err := ws.ExpandPath(types.Target{Path: "ws:api", Name: "build"})
	if err == nil {
		t.Fatal("expected error for ws:-prefixed path")
	}
}

// TestExpand_UnknownPath returns ErrUnknownProject for unknown paths.
func TestExpand_UnknownPath(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)
	_, err := ws.ExpandPath(types.Target{Path: "does/not/exist", Name: "build"})
	if err == nil {
		t.Fatal("expected error for unknown project path")
	}
	if !errors.Is(err, ErrUnknownProject) {
		t.Fatalf("error %q is not ErrUnknownProject", err)
	}
}

// TestParseTarget covers the canonical "target[:charm,...]" parsing cases.
func TestParseTarget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input      string
		wantName   string
		wantCharms []string
		wantErr    bool
	}{
		{input: "build", wantName: "build"}, // bare target
		{input: "lint:read", wantName: "lint", wantCharms: []string{"read"}},
		{input: "format:write", wantName: "format", wantCharms: []string{"write"}},
		{input: "", wantErr: true},
		{input: "lint:", wantErr: true},           // empty charm
		{input: "web/studio:test", wantErr: true}, // '/' not allowed in target
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got, err := types.ParseTarget(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseTarget(%q): want error, got %+v", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTarget(%q): unexpected error: %v", tc.input, err)
			}
			if got.Name != tc.wantName || !slices.Equal(got.Charms, tc.wantCharms) {
				t.Fatalf("ParseTarget(%q) = {Name:%q Charms:%v}, want {Name:%q Charms:%v}",
					tc.input, got.Name, got.Charms, tc.wantName, tc.wantCharms)
			}
		})
	}
}

// TestTargetString verifies the canonical "path:target" form.
func TestTargetString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		t    types.Target
		want string
	}{
		{types.Target{Path: "api", Name: "build"}, "api:build"},
		{types.Target{Name: "test"}, ":test"},
	}
	for _, tc := range cases {
		if got := tc.t.String(); got != tc.want {
			t.Errorf("Target%+v.String() = %q, want %q", tc.t, got, tc.want)
		}
	}
}

// TestMagus_proxies asserts that the methods *Magus exposes
// (Root, All, Get, Where, Affected, AffectedFromPaths, Graph,
// VCSOptions) return consistent values. The intent is to lock the
// public surface so internal refactors do not silently change behaviour.
func TestMagus_proxies(t *testing.T) {
	t.Parallel()
	ws := newWorkspace(t)

	if ws.Root() == "" {
		t.Fatal("Root: empty")
	}

	all := ws.All()
	if len(all) == 0 {
		t.Fatal("All: empty")
	}

	for _, p := range all {
		if got := ws.Get(p.Path); got == nil || got.Path != p.Path {
			t.Errorf("Get(%q): got %v, want project at %q", p.Path, got, p.Path)
		}
	}

	if got := ws.Get("does/not/exist"); got != nil {
		t.Errorf("Get(unknown): got %v, want nil", got)
	}

	if _, err := ws.Graph(); err != nil {
		t.Fatalf("Graph: %v", err)
	}

	// Where: pick the first non-root project. The root project
	// ("." marker) is deliberately not matched at the workspace root by
	// the underlying implementation (callers fall through to Select()).
	var nonRoot string
	for _, p := range all {
		if p.Path != "." {
			nonRoot = p.Path
			break
		}
	}
	if nonRoot == "" {
		t.Fatal("setup: no non-root project to test Where")
	}
	abs := filepath.Join(ws.Root(), nonRoot)
	if p, ok := ws.Where(abs); !ok || p.Path != nonRoot {
		t.Errorf("Where(%q): got (%v, %v), want project at %q", abs, p, ok, nonRoot)
	}

	// VCSOptions is a value-type accessor; it must be safe to call
	// without panicking on a freshly-opened workspace.
	_ = ws.VCSOptions()

	// AffectedFromPaths accepts an empty slice and returns a result with
	// no affected projects. We do not exercise Affected() here because it
	// shells out to git, which is not always available in test
	// environments.
	r, err := ws.AffectedFromPaths(context.Background(), nil)
	if err != nil {
		t.Fatalf("AffectedFromPaths(nil): %v", err)
	}
	if len(r.Affected) != 0 {
		t.Errorf("AffectedFromPaths(nil).Affected: got %v, want empty", r.Affected)
	}
}

func b64Pub(t *testing.T) (pub string, seed string) {
	t.Helper()
	pk, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(pk), base64.StdEncoding.EncodeToString(priv.Seed())
}

// TestRemoteCacheRequiresTrustSet is the paranoid root: a wired remote backend
// with no declared trust set must be a hard error, not a silent unverified cache.
func TestRemoteCacheRequiresTrustSet(t *testing.T) {
	if _, err := remoteCacheSigningOpts(nil); err == nil {
		t.Fatal("empty trust set was accepted; a remote cache must require trusted_keys")
	}
	if _, err := remoteCacheSigningOpts([]string{}); err == nil {
		t.Fatal("empty trust-set slice was accepted")
	}
}

// TestRemoteCacheTrustSetDecodes: a valid trust set yields verification options;
// adding a signing-key env var yields a signing option too.
func TestRemoteCacheTrustSetDecodes(t *testing.T) {
	pub, seed := b64Pub(t)

	opts, err := remoteCacheSigningOpts([]string{pub})
	if err != nil {
		t.Fatalf("valid trust set rejected: %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("verify-only: got %d opts, want 1 (trusted keys only)", len(opts))
	}

	t.Setenv(signingKeyEnv, seed)
	opts, err = remoteCacheSigningOpts([]string{pub})
	if err != nil {
		t.Fatalf("valid trust set + signing key rejected: %v", err)
	}
	if len(opts) != 2 {
		t.Fatalf("signing: got %d opts, want 2 (trusted keys + signing key)", len(opts))
	}
}

// TestRemoteCacheRejectsMalformedKeys: bad base64 in either the trust set or the
// signing-key env var is a clear configuration error, not a silent fallback.
func TestRemoteCacheRejectsMalformedKeys(t *testing.T) {
	if _, err := remoteCacheSigningOpts([]string{"not!base64!"}); err == nil {
		t.Error("malformed trusted key was accepted")
	}
	pub, _ := b64Pub(t)
	t.Setenv(signingKeyEnv, "not!base64!")
	if _, err := remoteCacheSigningOpts([]string{pub}); err == nil {
		t.Error("malformed signing key was accepted")
	}
}
