package describe

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nodeByName(nodes []types.TargetGraphNode, name string) (types.TargetGraphNode, bool) {
	for _, n := range nodes {
		if n.Name == name {
			return n, true
		}
	}
	return types.TargetGraphNode{}, false
}

func TestBuild(t *testing.T) {
	src := `import "magus";

// ── section divider ───────────────────────────────────────
// foo does a thing. This second sentence is dropped.
export fun foo_bar(args: [str]) > void {
    magus.needs(magus.target.literal("baz"));
    // magus.needs(magus.target.literal("ignored")) — a mention in a comment must not count
}

// separated by a blank line, so it must NOT attach

export fun baz(args: [str]) > void { magus.doctor([]); }

export fun gen_all(args: [str]) > void {
    magus.needs(magus.target.glob("*-gen"));
}

export fun a_gen(args: [str]) > void { go["x"](); }
`
	g := Extract(src)

	foo, ok := nodeByName(g, "foo-bar")
	require.True(t, ok, "missing foo-bar; got %v", g)
	assert.Equal(t, "foo does a thing.", foo.Doc)
	assert.Equal(t, []string{"baz"}, foo.Dependencies, "comment mention ignored")

	baz, _ := nodeByName(g, "baz")
	assert.Empty(t, baz.Doc, "blank line breaks contiguity")

	genAll, _ := nodeByName(g, "gen-all")
	assert.Equal(t, []string{"a-gen"}, genAll.Dependencies, "*-gen glob")
}

// TestCharms checks that a target's charm reads are extracted: the magus.has_charm
// names (including the built-in "rw"), sorted and deduped, while a has_charm
// mention in a comment or string does not count.
func TestCharms(t *testing.T) {
	g := Extract(`export fun build(args: [str]) > void {
    if (magus.has_charm("container")) { magus.needs(magus.target.literal("image-build")); }
    else { magus.needs(magus.target.literal("go-build")); }
}
export fun fmt(args: [str]) > void {
    if (magus.has_charm("rw")) { go["go-fmt"](); }
    // magus.has_charm("ignored") in a comment must not count
}
export fun plain(args: [str]) > void { go["x"](); }
`)
	build, _ := nodeByName(g, "build")
	assert.Equal(t, []string{"container"}, build.Charms)
	fmtNode, _ := nodeByName(g, "fmt")
	assert.Equal(t, []string{"rw"}, fmtNode.Charms, `has_charm("rw"), comment mention ignored`)
	plain, _ := nodeByName(g, "plain")
	assert.Empty(t, plain.Charms)
}

// TestInputsOutputs pins the per-target cache-footprint extraction: magus.inputs /
// magus.outputs string-literal globs are collected per target, a mention in a comment
// is ignored, and a target that declares neither carries empty sets.
func TestInputsOutputs(t *testing.T) {
	g := Extract(`export fun build(args: [str]) > void {
    magus.inputs("src/**", "tsconfig.json");
    magus.outputs("dist/**");
    // magus.inputs("ignored") in a comment must not count
}
export fun test(args: [str]) > void {
    magus.inputs("src/**");
}
export fun plain(args: [str]) > void { }
`)
	build, _ := nodeByName(g, "build")
	assert.Equal(t, []string{"src/**", "tsconfig.json"}, build.Inputs)
	assert.Equal(t, []string{"dist/**"}, build.Outputs)
	assert.False(t, build.DynamicIO)
	testNode, _ := nodeByName(g, "test")
	assert.Equal(t, []string{"src/**"}, testNode.Inputs)
	assert.Empty(t, testNode.Outputs)
	plain, _ := nodeByName(g, "plain")
	assert.Empty(t, plain.Inputs)
	assert.Empty(t, plain.Outputs)
}

// TestInputsOutputsDynamic pins the loud-rejection signal: a magus.inputs/outputs
// argument that is not a string literal sets DynamicIO (the load path turns that into
// an error), while any literal args in the same call are still collected.
func TestInputsOutputsDynamic(t *testing.T) {
	g := Extract(`export fun build(args: [str]) > void {
    final extra = "gen/**";
    magus.inputs("src/**", extra);
}
`)
	build, _ := nodeByName(g, "build")
	assert.True(t, build.DynamicIO, "a computed magus.inputs argument must flag DynamicIO")
	assert.Equal(t, []string{"src/**"}, build.Inputs, "literal args are still collected")
}

// TestUnreachedIO pins orphan detection: a magus.inputs/outputs reached from a target
// body (directly or via a bare-call helper) is NOT flagged, while one in an
// unreferenced helper or used as a value IS - it would never enter a cache key.
func TestUnreachedIO(t *testing.T) {
	orphans := UnreachedIO(`export fun build(args: [str]) > void {
    magus.inputs("src/**");
    helper();
}
fun helper() > void { magus.outputs("dist/**"); }
fun orphan() > void { magus.inputs("gen/**"); }
export fun test(args: [str]) > void {
    final f = magus.inputs;
    f("late/**");
}
`)
	// build's direct inputs and helper's outputs (bare-called) are reached -> not orphans.
	// orphan()'s inputs (never called) and test's `magus.inputs` value reference are.
	require.Len(t, orphans, 2)
	kinds := map[string]string{} // fn -> kind
	for _, o := range orphans {
		kinds[o.Fn] = o.Kind
	}
	assert.Equal(t, "inputs", kinds["orphan"], "magus.inputs in an uncalled helper is orphaned")
	assert.Equal(t, "inputs", kinds["test"], "magus.inputs used as a value is orphaned")
}

// TestSpellOps pins the per-target spell extraction: bracket (`go["go-test"]`) and
// dotted (`md.prettier(`) op calls are captured and grouped by spell, in call
// order, but only for handles a spell import brought into scope — a host call
// (os.exec) or a call on a non-spell identifier is dropped.
func TestSpellOps(t *testing.T) {
	g := Extract(`import "magus/spell/go";
import "magus/spell/md";
import "os";
export fun lint(args: [str]) > void {
    magus.needs(magus.target.literal("format"));
    go["golangci-lint"](); go["go-vet"](); go["golangci-lint"](); md.markdownlint();
}
export fun scan(args: [str]) > void { os.exec("trivy", []); other["x"](); }
`)
	lint, _ := nodeByName(g, "lint")
	want := []types.TargetSpellUse{
		{Spell: "go", Ops: []string{"golangci-lint", "go-vet"}}, // grouped, deduped, call order
		{Spell: "md", Ops: []string{"markdownlint"}},
	}
	assert.Equal(t, want, lint.Spells)
	// scan only calls a host module and an unknown identifier: no spell ops.
	scan, _ := nodeByName(g, "scan")
	assert.Empty(t, scan.Spells, "os.exec is host, other[] is not a spell")
}

// TestSpellOpsThroughHelper pins helper-following: a target that factors its spell
// ops, charms, and dependency edges into a same-file helper (image_build →
// build_variant → docker[...]/cosign[...]) keeps them attributed instead of
// silently dropping them. The helper is a plain (non-exported) fun, so it is not a
// node of its own; its work belongs to every target that calls it. A recursive
// helper must not loop (cycle guard), and a helper's own spell ops only attribute
// to callers, never leak between sibling targets.
func TestSpellOpsThroughHelper(t *testing.T) {
	g := Extract(`import "magus/spell/docker";
import "magus/spell/cosign";

fun build_variant(tag: str) > void {
    if (magus.has_charm("sign")) { cosign["cosign-sign"](); }
    docker["docker-buildx"]();
    self_loop();
}
fun self_loop() > void { self_loop(); }

export fun image_build(args: [str]) > void {
    magus.needs(magus.target.literal("preflight"));
    build_variant("latest");
}
export fun preflight(args: [str]) > void { go["x"](); }
`)
	img, ok := nodeByName(g, "image-build")
	require.True(t, ok, "missing image-build; got %v", g)
	wantSpells := []types.TargetSpellUse{
		{Spell: "cosign", Ops: []string{"cosign-sign"}},
		{Spell: "docker", Ops: []string{"docker-buildx"}},
	}
	assert.Equal(t, wantSpells, img.Spells, "ops through helper")
	assert.Equal(t, []string{"sign"}, img.Charms, "charm through helper")
	assert.Equal(t, []string{"preflight"}, img.Dependencies)
	// The helper's ops belong only to callers; a sibling that never calls it stays clean.
	pf, _ := nodeByName(g, "preflight")
	assert.Empty(t, pf.Spells, "helper ops must not leak between siblings")
}

// TestSpellOpsIgnoresStringLiterals pins that a call-form token appearing as free
// text inside a string literal — an echo/help/error message — does not register a
// phantom spell op. Only the op string of a real bracket call is read.
func TestSpellOpsIgnoresStringLiterals(t *testing.T) {
	g := Extract(`import "magus/spell/go";
export fun help(args: [str]) > void {
    os.exec("echo", ["run go.fmt() then go[\"go-test\"]() yourself"]);
    go["go-build"]();
}
`)
	help, _ := nodeByName(g, "help")
	// The only real call is go["go-build"](); the go.fmt()/go["go-test"] mentions
	// live inside the echo string and must be ignored.
	want := []types.TargetSpellUse{{Spell: "go", Ops: []string{"go-build"}}}
	assert.Equal(t, want, help.Spells, "string-literal mentions must not count")
}

// TestNameNormalization pins the fix for the node-vs-edge name mismatch: node
// names and depends_on names must both be normalized the way the run path
// registers targets (kebab-case), so a camelCase function and a hyphenated
// dependency reconcile.
func TestNameNormalization(t *testing.T) {
	g := Extract(`export fun goBuild(args: [str]) > void { go["x"](); }
export fun ci(args: [str]) > void { magus.needs(magus.target.literal("goBuild")); }
`)
	_, ok := nodeByName(g, "go-build")
	require.True(t, ok, "camelCase goBuild should normalize to go-build; got %v", g)
	ci, _ := nodeByName(g, "ci")
	assert.Equal(t, []string{"go-build"}, ci.Dependencies, "dep name normalized to match node")
}

// TestBraceInString guards collectBody: a `}` inside a string literal must not
// truncate the body and drop the depends_on that follows it.
func TestBraceInString(t *testing.T) {
	g := Extract(`export fun build(args: [str]) > void {
    os.exec("sh", ["-c", "echo }"]);
    magus.needs(magus.target.literal("fmt"));
}
export fun fmt(args: [str]) > void { go["x"](); }
`)
	build, _ := nodeByName(g, "build")
	assert.Equal(t, []string{"fmt"}, build.Dependencies, "brace in string must not truncate body")
}

// TestTrailingComment guards codeBody: a depends_on in a trailing inline comment
// is prose, not an edge.
func TestTrailingComment(t *testing.T) {
	g := Extract(`export fun build(args: [str]) > void {
    magus.needs(magus.target.literal("real")); // magus.needs(magus.target.literal("fake"))
}
export fun real(args: [str]) > void { go["x"](); }
`)
	build, _ := nodeByName(g, "build")
	assert.Equal(t, []string{"real"}, build.Dependencies, "trailing comment ignored")
}

// TestNeedsGlobMultiPattern guards multi-handle needs: each magus.target.glob in a
// every pattern in it must be honored, not just the first.
func TestNeedsGlobMultiPattern(t *testing.T) {
	g := Extract(`export fun all(args: [str]) > void {
    magus.needs(magus.target.glob("*-gen"), magus.target.glob("check-*"));
}
export fun docs_gen(args: [str]) > void { go["x"](); }
export fun check_lint(args: [str]) > void { go["x"](); }
`)
	all, _ := nodeByName(g, "all")
	want := []string{"docs-gen", "check-lint"}
	assert.Equal(t, want, all.Dependencies, "both glob patterns honored")
}

// TestNeedsHandles guards the magus.needs handle edges: named is an exact dep,
// glob and regex resolve against sibling target names, a multi-arg needs call
// yields every edge (per-leaf extraction, not call-spanning), and a handle in a
// trailing comment is prose, not an edge.
func TestNeedsHandles(t *testing.T) {
	g := Extract(`export fun build(args: [str]) > void { go["x"](); }
export fun a_gen(args: [str]) > void { go["x"](); }
export fun b_gen(args: [str]) > void { go["x"](); }
export fun test(args: [str]) > void {
    magus.needs(magus.target.literal("build"));
    magus.needs(magus.target.glob("*-gen"), magus.target.regex("^b-"));
    // magus.target.literal("ignored") in a comment must not count
}
`)
	test, ok := nodeByName(g, "test")
	require.True(t, ok, "missing test; got %v", g)
	want := []string{"build", "a-gen", "b-gen"}
	assert.Equal(t, want, test.Dependencies, "named exact + glob + regex; comment ignored")
}

func TestExternalCrossDependencies(t *testing.T) {
	g := Extract(`import "project/../gopherbuzz" as gopherbuzz;
export fun build_playground(args: [str]) > void {
    magus.needs(magus.target.literal("preflight"));
    magus.needs(gopherbuzz.build);
    // gopherbuzz.ignored in a comment must not count
}
export fun preflight(args: [str]) > void { go["x"](); }
`)
	bp, ok := nodeByName(g, "build-playground")
	require.True(t, ok, "missing build-playground; got %v", g)
	// The cross-project edge is a CrossDependency (project + target), not a same-project
	// dependency; the project path is left raw for the caller to resolve.
	want := []types.CrossTargetRef{{Project: "../gopherbuzz", Target: "build"}}
	assert.Equal(t, want, bp.CrossDependencies)
	assert.Equal(t, []string{"preflight"}, bp.Dependencies, "external is not a same-project dep")
}

// TestDependencyTokensInStringLiterals ensures dependency-edge tokens that appear
// inside string literals (not code) are ignored — they must not register phantom
// edges, which for an external edge would pollute the affected set.
func TestDependencyTokensInStringLiterals(t *testing.T) {
	g := Extract(`import "project/../api" as api;
export fun build(args: [str]) > void {
    magus.info("run magus.needs(magus.target.literal(\"setup\")) and api.compile first");
    go["go-build"]();
}
export fun setup(args: [str]) > void { go["x"](); }
`)
	b, ok := nodeByName(g, "build")
	require.True(t, ok, "missing build; got %v", g)
	assert.Empty(t, b.Dependencies, "the literal handle is inside a string literal")
	assert.Empty(t, b.CrossDependencies, "the cross-project ref is inside a string literal")
}

func TestCycle(t *testing.T) {
	acyclic := Extract(`export fun a(args: [str]) > void { magus.needs(magus.target.literal("b")); }
export fun b(args: [str]) > void { magus.needs(magus.target.literal("c")); }
export fun c(args: [str]) > void { go["x"](); }
`)
	assert.Nil(t, Cycle(acyclic), "acyclic graph reported cycle")

	cyclic := Extract(`export fun a(args: [str]) > void { magus.needs(magus.target.literal("b")); }
export fun b(args: [str]) > void { magus.needs(magus.target.literal("c")); }
export fun c(args: [str]) > void { magus.needs(magus.target.literal("a")); }
`)
	c := Cycle(cyclic)
	require.NotNil(t, c, "cyclic graph reported no cycle")
	assert.Equal(t, c[0], c[len(c)-1], "cycle should start and end at the same node")
}

// TestCycleAcrossNormalization is the regression for the silent-pass bug: a real
// cycle written with mixed casing must still be detected once both sides are
// normalized.
func TestCycleAcrossNormalization(t *testing.T) {
	g := Extract(`export fun aB(args: [str]) > void { magus.needs(magus.target.literal("bC")); }
export fun bC(args: [str]) > void { magus.needs(magus.target.literal("aB")); }
`)
	assert.NotNil(t, Cycle(g), "mixed-case cycle aB→bC→aB not detected")
}
