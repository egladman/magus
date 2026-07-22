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
export fun foo_bar(ctx: magus\Context, args: [str]) > void {
    ctx.needs(baz);
    // ctx.needs(baz) — a mention in a comment must not count
}

// separated by a blank line, so it must NOT attach

export fun baz(ctx: magus\Context, args: [str]) > void { magus.doctor([]); }

export fun gen_all(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("*-gen"));
}

export fun a_gen(ctx: magus\Context, args: [str]) > void { go["x"](); }
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
	g := Extract(`export fun build(ctx: magus\Context, args: [str]) > void {
    if (ctx.has_charm("container")) { ctx.needs(image_build); }
    else { ctx.needs(go_build); }
}
export fun fmt(ctx: magus\Context, args: [str]) > void {
    if (ctx.has_charm("rw")) { go["go-fmt"](); }
    // ctx.has_charm("ignored") in a comment must not count
}
export fun plain(ctx: magus\Context, args: [str]) > void { go["x"](); }
`)
	build, _ := nodeByName(g, "build")
	assert.Equal(t, []string{"container"}, build.Charms)
	fmtNode, _ := nodeByName(g, "fmt")
	assert.Equal(t, []string{"rw"}, fmtNode.Charms, `has_charm("rw"), comment mention ignored`)
	plain, _ := nodeByName(g, "plain")
	assert.Empty(t, plain.Charms)
}

// TestCtxFormCharms checks that a ctx-form target's ctx.has_charm reads are extracted
// too, so the static charm inventory (the doctor's charm/target-collision and
// has_charm-typo checks) sees them - the receiver is ctx, not magus, but the charm
// name is a real read all the same.
func TestCtxFormCharms(t *testing.T) {
	g := Extract(`import "magus";
export fun release(ctx: magus\Context, args: [str]) > void {
    if (ctx.has_charm("cd")) { ctx.outputs("dist/pkg.tar.gz"); }
}
`)
	release, _ := nodeByName(g, "release")
	assert.Equal(t, []string{"cd"}, release.Charms, "ctx.has_charm names are extracted statically")
}

// TestHasCharmBothReceivers pins that has_charm is read through BOTH receivers: the
// still-live magus.has_charm global query and the ctx.has_charm form. Unlike
// needs/inputs/outputs (ctx-only now), has_charm keeps its global, so a target reading
// either must contribute the same charm to the static inventory - or the doctor charm
// checks and the MAGUS.md listing would silently miss a magus.has_charm read.
func TestHasCharmBothReceivers(t *testing.T) {
	viaCtx := Extract(`import "magus";
export fun build(ctx: magus\Context, args: [str]) > void { if (ctx.has_charm("container")) {} }
`)
	viaMagus := Extract(`import "magus";
export fun build(ctx: magus\Context, args: [str]) > void { if (magus.has_charm("container")) {} }
`)
	c, _ := nodeByName(viaCtx, "build")
	m, _ := nodeByName(viaMagus, "build")
	assert.Equal(t, []string{"container"}, c.Charms, "ctx.has_charm read is extracted")
	assert.Equal(t, c.Charms, m.Charms, "magus.has_charm must yield the identical charm inventory")
}

// TestCtxFormCharmBranchSeesBothArms is the regression guard for the reason the graph
// is read statically rather than by running the body: a target whose dependency is
// gated on a charm declares a DIFFERENT edge per branch. The static read sees BOTH
// (image-build AND go-build); a run/discovery under one charm configuration would see
// only the arm it took, silently dropping the other edge from the graph and the
// affected set.
func TestCtxFormCharmBranchSeesBothArms(t *testing.T) {
	g := Extract(`import "magus";
export fun build(ctx: magus\Context, args: [str]) > void {
    if (ctx.has_charm("container")) {
        ctx.needs(image_build);
    } else {
        ctx.needs(go_build);
    }
}
export fun image_build(ctx: magus\Context, args: [str]) > void {}
export fun go_build(ctx: magus\Context, args: [str]) > void {}
`)
	build, ok := nodeByName(g, "build")
	require.True(t, ok, "build node present")
	assert.ElementsMatch(t, []string{"image-build", "go-build"}, build.Dependencies,
		"both arms of the ctx.has_charm branch must be edges, not just the arm a run would take")
}

// TestInputsOutputs pins the per-target cache-footprint extraction: magus.inputs /
// magus.outputs string-literal globs are collected per target, a mention in a comment
// is ignored, and a target that declares neither carries empty sets.
func TestInputsOutputs(t *testing.T) {
	g := Extract(`export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.inputs("src/**", "tsconfig.json");
    ctx.outputs("dist/**");
    // ctx.inputs("ignored") in a comment must not count
}
export fun test(ctx: magus\Context, args: [str]) > void {
    ctx.inputs("src/**");
}
export fun plain(ctx: magus\Context, args: [str]) > void { }
`)
	build, _ := nodeByName(g, "build")
	// A bare-literal glob is a same-project input: empty Project (meaning "this target's
	// own project", filled at resolution), Rel the glob.
	assert.Equal(t, []types.InputRef{{Glob: "src/**"}, {Glob: "tsconfig.json"}}, build.Inputs)
	assert.Equal(t, []string{"dist/**"}, build.Outputs)
	assert.False(t, build.DynamicIO)
	testNode, _ := nodeByName(g, "test")
	assert.Equal(t, []types.InputRef{{Glob: "src/**"}}, testNode.Inputs)
	assert.Empty(t, testNode.Outputs)
	plain, _ := nodeByName(g, "plain")
	assert.Empty(t, plain.Inputs)
	assert.Empty(t, plain.Outputs)
}

// TestInputsOutputsDynamic pins the loud-rejection signal: a magus.inputs/outputs
// argument that is not a string literal sets DynamicIO (the load path turns that into
// an error), while any literal args in the same call are still collected.
func TestInputsOutputsDynamic(t *testing.T) {
	g := Extract(`export fun build(ctx: magus\Context, args: [str]) > void {
    final extra = "gen/**";
    ctx.inputs("src/**", extra);
}
`)
	build, _ := nodeByName(g, "build")
	assert.True(t, build.DynamicIO, "a computed magus.inputs argument must flag DynamicIO")
	assert.Equal(t, []types.InputRef{{Glob: "src/**"}}, build.Inputs, "literal args are still collected")
}

// TestUnreachedIO pins orphan detection: a ctx.inputs/outputs reached from a target
// body (directly or via a bare-call helper) is NOT flagged, while one in an
// unreferenced helper or used as a value IS - it would never enter a cache key.
func TestUnreachedIO(t *testing.T) {
	orphans := UnreachedIO(`export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.inputs("src/**");
    helper();
}
fun helper() > void { ctx.outputs("dist/**"); }
fun orphan() > void { ctx.inputs("gen/**"); }
export fun test(ctx: magus\Context, args: [str]) > void {
    final f = ctx.inputs;
    f("late/**");
}
`)
	// build's direct inputs and helper's outputs (bare-called) are reached -> not orphans.
	// orphan()'s inputs (never called) and test's `ctx.inputs` value reference are.
	require.Len(t, orphans, 2)
	kinds := map[string]string{} // fn -> kind
	for _, o := range orphans {
		kinds[o.Fn] = o.Kind
	}
	assert.Equal(t, "inputs", kinds["orphan"], "ctx.inputs in an uncalled helper is orphaned")
	assert.Equal(t, "inputs", kinds["test"], "ctx.inputs used as a value is orphaned")
}

// TestSpellOps pins the per-target spell extraction: bracket (`go["go-test"]`) and
// dotted (`md.prettier(`) op calls are captured and grouped by spell, in call
// order, but only for handles a spell import brought into scope — a host call
// (os.exec) or a call on a non-spell identifier is dropped.
func TestSpellOps(t *testing.T) {
	g := Extract(`import "magus/spell/go";
import "magus/spell/md";
import "os";
export fun format(ctx: magus\Context, args: [str]) > void { go["go-fmt"](); }
export fun lint(ctx: magus\Context, args: [str]) > void {
    ctx.needs(format);
    go["golangci-lint"](); go["go-vet"](); go["golangci-lint"](); md.markdownlint();
}
export fun scan(ctx: magus\Context, args: [str]) > void { os.exec("trivy", []); other["x"](); }
`)
	lint, _ := nodeByName(g, "lint")
	want := []types.TargetSpellUse{
		{Spell: "go", Ops: []string{"golangci-lint", "go-vet"}}, // grouped, deduped, call order
		{Spell: "md", Ops: []string{"markdownlint"}},
	}
	assert.Equal(t, want, lint.Spells)
	assert.Equal(t, []string{"format"}, lint.Dependencies, "the identifier edge resolves to the exported target")
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
    if (ctx.has_charm("sign")) { cosign["cosign-sign"](); }
    docker["docker-buildx"]();
    self_loop();
}
fun self_loop() > void { self_loop(); }

export fun image_build(ctx: magus\Context, args: [str]) > void {
    ctx.needs(preflight);
    build_variant("latest");
}
export fun preflight(ctx: magus\Context, args: [str]) > void { go["x"](); }
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
export fun help(ctx: magus\Context, args: [str]) > void {
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
// names and dependency identifiers must both be normalized the way the run path
// registers targets (kebab-case), so a camelCase function and a hyphenated
// node reconcile.
func TestNameNormalization(t *testing.T) {
	g := Extract(`export fun goBuild(ctx: magus\Context, args: [str]) > void { go["x"](); }
export fun ci(ctx: magus\Context, args: [str]) > void { ctx.needs(goBuild); }
`)
	_, ok := nodeByName(g, "go-build")
	require.True(t, ok, "camelCase goBuild should normalize to go-build; got %v", g)
	ci, _ := nodeByName(g, "ci")
	assert.Equal(t, []string{"go-build"}, ci.Dependencies, "dep name normalized to match node")
}

// TestBraceInString guards that a `}` inside a string literal does not truncate the
// AST body and drop the magus.needs edge that follows it.
func TestBraceInString(t *testing.T) {
	g := Extract(`export fun build(ctx: magus\Context, args: [str]) > void {
    os.exec("sh", ["-c", "echo }"]);
    ctx.needs(fmt);
}
export fun fmt(ctx: magus\Context, args: [str]) > void { go["x"](); }
`)
	build, _ := nodeByName(g, "build")
	assert.Equal(t, []string{"fmt"}, build.Dependencies, "brace in string must not truncate body")
}

// TestTrailingComment guards that a magus.needs handle in a trailing inline comment
// is prose, not an edge.
func TestTrailingComment(t *testing.T) {
	g := Extract(`export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.needs(real); // ctx.needs(fake)
}
export fun real(ctx: magus\Context, args: [str]) > void { go["x"](); }
`)
	build, _ := nodeByName(g, "build")
	assert.Equal(t, []string{"real"}, build.Dependencies, "trailing comment ignored")
}

// TestNeedsGlobMultiPattern guards multi-pattern glob: every pattern in a
// single magus.glob call must be honored, not just the first.
func TestNeedsGlobMultiPattern(t *testing.T) {
	g := Extract(`export fun all(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("*-gen", "check-*"));
}
export fun docs_gen(ctx: magus\Context, args: [str]) > void { go["x"](); }
export fun check_lint(ctx: magus\Context, args: [str]) > void { go["x"](); }
`)
	all, _ := nodeByName(g, "all")
	want := []string{"docs-gen", "check-lint"}
	assert.Equal(t, want, all.Dependencies, "both glob patterns honored")
}

// TestNeedsHandles guards magus.needs / magus.glob edges: an identifier naming
// an exported target is an exact dep, glob patterns resolve against sibling
// target names (a starless pattern is suffix shorthand), a multi-pattern glob
// yields every match, and a handle in a trailing comment is prose, not an edge.
func TestNeedsHandles(t *testing.T) {
	g := Extract(`export fun build(ctx: magus\Context, args: [str]) > void { go["x"](); }
export fun a_gen(ctx: magus\Context, args: [str]) > void { go["x"](); }
export fun b_gen(ctx: magus\Context, args: [str]) > void { go["x"](); }
export fun test(ctx: magus\Context, args: [str]) > void {
    ctx.needs(build);
    ctx.needs(ctx.glob("*-gen", "b-*"));
    // ctx.needs(ignored) in a comment must not count
}
`)
	test, ok := nodeByName(g, "test")
	require.True(t, ok, "missing test; got %v", g)
	want := []string{"build", "a-gen", "b-gen"}
	assert.Equal(t, want, test.Dependencies, "identifier exact + glob patterns; comment ignored")
}

func TestExternalCrossDependencies(t *testing.T) {
	g := Extract(`import "project/../gopherbuzz" as gopherbuzz;
export fun build_playground(ctx: magus\Context, args: [str]) > void {
    ctx.needs(preflight);
    ctx.needs(gopherbuzz.build);
    // gopherbuzz.ignored in a comment must not count
}
export fun preflight(ctx: magus\Context, args: [str]) > void { go["x"](); }
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
export fun build(ctx: magus\Context, args: [str]) > void {
    magus.info("run ctx.needs(setup) and api.compile first");
    go["go-build"]();
}
export fun setup(ctx: magus\Context, args: [str]) > void { go["x"](); }
`)
	b, ok := nodeByName(g, "build")
	require.True(t, ok, "missing build; got %v", g)
	assert.Empty(t, b.Dependencies, "the identifier handle is inside a string literal")
	assert.Empty(t, b.CrossDependencies, "the cross-project ref is inside a string literal")
}

// TestCrossFileInputs: ctx.inputs(<alias>.file("lit")) and a sibling bare string
// literal land on the SAME Inputs list in one shape - the literal a same-project input
// (empty Project), the alias.file a cross-project input (raw dep path + rel); the
// recognized cross-file arg does NOT trip DynamicIO, and the .file member mints no
// phantom cross-dependency. Same-project entries come first (arg order), cross after.
func TestCrossFileInputs(t *testing.T) {
	g := Extract(`import "project/../lib" as lib;
export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.inputs(lib.file("go.mod"), "src/**/*.go");
    go["go-build"]();
}
`)
	b, ok := nodeByName(g, "build")
	require.True(t, ok, "missing build; got %v", g)
	assert.Equal(t, []types.InputRef{
		{Glob: "src/**/*.go"},               // same-project literal: empty Project (self)
		{Project: "../lib", Glob: "go.mod"}, // cross-project: raw dep path for the caller to resolve
	}, b.Inputs, "a literal and an alias.file input share one representation")
	assert.False(t, b.DynamicIO, "a recognized alias.file(lit) arg must not trip DynamicIO")
	assert.Empty(t, b.CrossDependencies, "the reserved .file member mints no phantom cross-dependency")
}

// TestCrossFileInputsDynamic: a computed (non-literal) rel in alias.file(...) is invisible
// to the static read, so it trips DynamicIO exactly like any other non-literal io arg.
func TestCrossFileInputsDynamic(t *testing.T) {
	g := Extract(`import "project/../lib" as lib;
export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.inputs(lib.file(args[0]));
    go["go-build"]();
}
`)
	b, ok := nodeByName(g, "build")
	require.True(t, ok, "missing build; got %v", g)
	assert.True(t, b.DynamicIO, "a computed rel is invisible to the static read and must trip DynamicIO")
	assert.Empty(t, b.Inputs, "a non-literal rel contributes no input")
}

func TestCycle(t *testing.T) {
	acyclic := Extract(`export fun a(ctx: magus\Context, args: [str]) > void { ctx.needs(b); }
export fun b(ctx: magus\Context, args: [str]) > void { ctx.needs(c); }
export fun c(ctx: magus\Context, args: [str]) > void { go["x"](); }
`)
	assert.Nil(t, Cycle(acyclic), "acyclic graph reported cycle")

	cyclic := Extract(`export fun a(ctx: magus\Context, args: [str]) > void { ctx.needs(b); }
export fun b(ctx: magus\Context, args: [str]) > void { ctx.needs(c); }
export fun c(ctx: magus\Context, args: [str]) > void { ctx.needs(a); }
`)
	c := Cycle(cyclic)
	require.NotNil(t, c, "cyclic graph reported no cycle")
	assert.Equal(t, c[0], c[len(c)-1], "cycle should start and end at the same node")
}

// TestCycleAcrossNormalization is the regression for the silent-pass bug: a real
// cycle written with mixed casing must still be detected once both sides are
// normalized.
func TestCycleAcrossNormalization(t *testing.T) {
	g := Extract(`export fun aB(ctx: magus\Context, args: [str]) > void { ctx.needs(bC); }
export fun bC(ctx: magus\Context, args: [str]) > void { ctx.needs(aB); }
`)
	assert.NotNil(t, Cycle(g), "mixed-case cycle aB→bC→aB not detected")
}

// TestDeclaredName checks the raw as-written target name is captured when the
// normalizer rewrites it, and left empty when the spelling already matches.
func TestDeclaredName(t *testing.T) {
	g := Extract(`export fun goBuild(ctx: magus\Context, args: [str]) > void {}
export fun build(ctx: magus\Context, args: [str]) > void {}
`)
	rewritten, _ := nodeByName(g, "go-build")
	assert.Equal(t, "goBuild", rewritten.Declared, "camelCase name captured as declared")
	plain, _ := nodeByName(g, "build")
	assert.Empty(t, plain.Declared, "a name the normalizer leaves alone has no declared_as")
}
