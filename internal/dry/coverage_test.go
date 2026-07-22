package dry

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// traceDetail concatenates the Detail of every op in a Result's trace, so a test
// can assert a stubbed host member ran without caring about op ordering.
func traceDetail(r Result) string {
	var b strings.Builder
	for _, op := range r.Trace {
		b.WriteString(op.Detail)
		b.WriteByte(' ')
	}
	return b.String()
}

// TestPlaygroundHostModules asserts the playground manifest lists every
// WASM-compatible module plus the wired-as-a-global "magus".
func TestPlaygroundHostModules(t *testing.T) {
	got := PlaygroundHostModules()
	assert.Contains(t, got, "magus", "magus is wired as a global and must be listed")
	assert.Contains(t, got, "strings", "a known WASM-compatible module must be listed")
	assert.Len(t, got, len(WASMCompatibleMagusModules)+1, "manifest is the wasm set plus magus")
	var magusCount int
	for _, m := range got {
		if m == "magus" {
			magusCount++
		}
	}
	assert.Equal(t, 1, magusCount, "magus appears exactly once (it is not double-listed from the wasm set)")
}

// TestEvalInContext_expression evaluates a bare expression against an autoloaded
// magusfile's top-level definitions, like a REPL.
func TestEvalInContext_expression(t *testing.T) {
	const src = `fun ldflags(v: str) > str => "-X main.version=" + v;`
	r := EvalInContext(context.Background(), src, `ldflags("1.0")`)
	require.True(t, r.OK, "eval-in-context failed: %+v", r.Diag)
	assert.Contains(t, r.Result, "1.0", "the expression should see the file's top-level fun")
}

// TestEvalInContext_statementFallback drives the statement-form compile fallback:
// a var declaration is not an expression, so `return <expr>` fails to compile and
// the plain statement form runs instead.
func TestEvalInContext_statementFallback(t *testing.T) {
	r := EvalInContext(context.Background(), "", "var x = 41 + 1;")
	require.True(t, r.OK, "statement form should compile and run: %+v", r.Diag)
}

// TestEvalInContext_selfContainedDespiteBrokenFile: a magusfile that fails to
// compile binds nothing, but a self-contained expression still evaluates.
func TestEvalInContext_selfContainedDespiteBrokenFile(t *testing.T) {
	r := EvalInContext(context.Background(), "this is not valid buzz", "return 2 + 3;")
	require.True(t, r.OK, "a self-contained expr should evaluate despite a broken file: %+v", r.Diag)
	assert.Equal(t, "5", r.Result)
}

// TestEvalInContext_compileError surfaces a diag when neither the expression nor
// the statement form compiles.
func TestEvalInContext_compileError(t *testing.T) {
	r := EvalInContext(context.Background(), "", "1 +")
	assert.False(t, r.OK, "an un-compilable expression must fail")
	require.NotNil(t, r.Diag)
	assert.NotEmpty(t, r.Diag.Msg)
}

// TestTraceProject_unknownKeyHint drives rejectUnknownKeys through
// suggestNearest/levenshtein: a near-miss project option key yields a "did you
// mean" hint naming the intended key.
func TestTraceProject_unknownKeyHint(t *testing.T) {
	const src = `magus.project({"outputz": ["bin/**"]});`
	g := LoadMagusfile(context.Background(), src)
	require.False(t, g.OK, "an unknown project option must fail the load")
	require.NotNil(t, g.Diag)
	assert.Contains(t, g.Diag.Msg, `unknown option "outputz"`)
	assert.Contains(t, g.Diag.Msg, `did you mean "outputs"`, "a distance-1 typo gets a suggestion")
}

// TestTraceProject_unknownKeyNoHint: a key far from every known option lists the
// known options but offers no suggestion.
func TestTraceProject_unknownKeyNoHint(t *testing.T) {
	const src = `magus.project({"zzzzzzzz": true});`
	g := LoadMagusfile(context.Background(), src)
	require.False(t, g.OK)
	require.NotNil(t, g.Diag)
	assert.Contains(t, g.Diag.Msg, `unknown option "zzzzzzzz"`)
	assert.NotContains(t, g.Diag.Msg, "did you mean", "nothing is close enough to suggest")
	assert.Contains(t, g.Diag.Msg, "known options:", "the message still enumerates the valid keys")
}

// TestTraceProject_unknownTargetPolicyKey exercises the per-target policy
// rejectUnknownKeys path (a distinct call site from the top-level options).
func TestTraceProject_unknownTargetPolicyKey(t *testing.T) {
	const src = `magus.project({"targets": {"lint": {"skipcache": true}}});`
	g := LoadMagusfile(context.Background(), src)
	require.False(t, g.OK)
	require.NotNil(t, g.Diag)
	assert.Contains(t, g.Diag.Msg, `targets["lint"]`, "the error names the offending target")
	assert.Contains(t, g.Diag.Msg, `did you mean "skip_cache"`)
}

// TestTraceProject_slotsNotInt: a non-integer slots value is rejected with a
// type-shaped message.
func TestTraceProject_slotsNotInt(t *testing.T) {
	const src = `magus.project({"targets": {"lint": {"slots": "four"}}});`
	g := LoadMagusfile(context.Background(), src)
	require.False(t, g.OK)
	require.NotNil(t, g.Diag)
	assert.Contains(t, g.Diag.Msg, "slots must be a whole number")
}

// TestTraceProject_slotsBelowOne: slots must be >= 1.
func TestTraceProject_slotsBelowOne(t *testing.T) {
	const src = `magus.project({"targets": {"lint": {"slots": 0}}});`
	g := LoadMagusfile(context.Background(), src)
	require.False(t, g.OK)
	require.NotNil(t, g.Diag)
	assert.Contains(t, g.Diag.Msg, "slots must be >= 1")
}

// TestTraceProject_exclusiveTargetAndBools flattens the exclusive per-target
// policy plus the top-level exclusive/depends_on/sources fields into the project.
func TestTraceProject_exclusiveTargetAndBools(t *testing.T) {
	const src = `magus.project({
    "exclusive": true,
    "depends_on": ["../lib"],
    "sources": ["src/**"],
    "outputs": "bin/app",
    "targets": {"deploy": {"exclusive": true}},
});
export fun deploy(ctx: magus\Context, args: [str]) > void {}`
	g := LoadMagusfile(context.Background(), src)
	require.True(t, g.OK, "load failed: %+v", g.Diag)
	require.Len(t, g.Projects, 1)
	p := g.Projects[0]
	assert.True(t, p.Exclusive)
	assert.Equal(t, []string{"../lib"}, p.DependsOn)
	assert.Equal(t, []string{"src/**"}, p.Sources)
	assert.Equal(t, []string{"bin/app"}, p.Outputs, "a bare str outputs coerces to a one-element list")
	assert.Equal(t, []string{"deploy"}, p.ExclusiveTargets)
}

// TestTraceProject_malformedCall: a non-map, non-str argument is a no-op config
// (captureConfigure returns a null opts), so the project registers with defaults.
func TestTraceProject_malformedCall(t *testing.T) {
	const src = `magus.project(5);`
	g := LoadMagusfile(context.Background(), src)
	require.True(t, g.OK, "a malformed magus.project should be a lenient no-op: %+v", g.Diag)
	require.Len(t, g.Projects, 1)
	assert.Equal(t, ".", g.Projects[0].Path, "path defaults to the workspace root")
}

// TestTraceProject_explicitPath: the two-argument form sets an explicit project
// path; a non-map second argument degrades to no options.
func TestTraceProject_explicitPath(t *testing.T) {
	g := LoadMagusfile(context.Background(), `magus.project("./sub", {"outputs": ["out/**"]});`)
	require.True(t, g.OK, "load failed: %+v", g.Diag)
	require.Len(t, g.Projects, 1)
	assert.Equal(t, "./sub", g.Projects[0].Path)
	assert.Equal(t, []string{"out/**"}, g.Projects[0].Outputs)

	g2 := LoadMagusfile(context.Background(), `magus.project("./sub", 5);`)
	require.True(t, g2.OK, "load failed: %+v", g2.Diag)
	require.Len(t, g2.Projects, 1)
	assert.Equal(t, "./sub", g2.Projects[0].Path)
	assert.Empty(t, g2.Projects[0].Outputs, "a non-map opts is treated as no options")
}

// TestTraceProject_valToStringsShapes covers valToStrings' non-str/non-list
// branch (a scalar depends_on is dropped) and its list branch skipping non-str
// items.
func TestTraceProject_valToStringsShapes(t *testing.T) {
	const src = `magus.project({"depends_on": 5, "outputs": [1, "keep", true]});`
	g := LoadMagusfile(context.Background(), src)
	require.True(t, g.OK, "load failed: %+v", g.Diag)
	require.Len(t, g.Projects, 1)
	assert.Empty(t, g.Projects[0].DependsOn, "a scalar depends_on yields no strings")
	assert.Equal(t, []string{"keep"}, g.Projects[0].Outputs, "non-str list items are skipped")
}

// TestRun_globNoStarPattern drives globToRegexp's no-star branch: a bare pattern
// matches any target ending in -<pattern>.
func TestRun_globNoStarPattern(t *testing.T) {
	const src = `
export fun proto_generate(ctx: magus\Context, args: [str]) > void {}
export fun mock_generate(ctx: magus\Context, args: [str]) > void {}
export fun generate(ctx: magus\Context, args: [str]) > void { ctx.needs(ctx.glob("generate")); }
`
	g := LoadMagusfile(context.Background(), src)
	require.True(t, g.OK, "load failed: %+v", g.Diag)
	var deps []string
	for _, e := range g.Edges {
		if e.From == "generate" {
			deps = append(deps, e.To)
		}
	}
	slices.Sort(deps)
	assert.Equal(t, []string{"mock-generate", "proto-generate"}, deps,
		"a starless glob matches any -generate target (but not generate itself)")
}

// TestRun_stubbedHostMembers probes the buildMagus stubs the dry run does not
// model into the graph: the log levels (traced as per-target ops), the
// captured-command members, module introspection, and the runtime-only no-ops.
// None may blow up when a target body calls them.
func TestRun_stubbedHostMembers(t *testing.T) {
	const src = `
import "magus";
export fun work(ctx: magus\Context, args: [str]) > void {
    magus.info("i");
    magus.warn("w");
    magus.error("e");
    magus.debug("d");
    magus.cmd(["ls"]);
    magus.describe(["x"]);
    magus.insight(["y"]);
    magus.doctor(["z"]);
    magus.modules();
    magus.module("go");
    magus.hint("h");
    magus.pry();
    magus.bustCache();
}
`
	r := Run(context.Background(), src, "work", nil)
	require.True(t, r.OK, "dry-run failed: %+v", r.Diag)
	levels := map[string]bool{}
	for _, op := range r.Trace {
		if op.Kind == "log" {
			levels[op.Name] = true
		}
	}
	for _, want := range []string{"info", "warn", "error", "debug"} {
		assert.True(t, levels[want], "log level %q should trace as an op; got %+v", want, r.Trace)
	}
}

// TestRun_logMissingArg drives strArg's fallback: magus.info() with no argument
// traces an empty-detail log op rather than panicking.
func TestRun_logMissingArg(t *testing.T) {
	const src = `
import "magus";
export fun work(ctx: magus\Context, args: [str]) > void { magus.info(); }
`
	r := Run(context.Background(), src, "work", nil)
	require.True(t, r.OK, "dry-run failed: %+v", r.Diag)
	require.Len(t, r.Trace, 1)
	assert.Equal(t, "log", r.Trace[0].Kind)
	assert.Empty(t, r.Trace[0].Detail, "a missing log argument falls back to empty")
}

// TestLoadMagusfile_topLevelLogNoAttribution: a log call at the top level (no
// current target) has nowhere to attribute, so addOp drops it without erroring.
func TestLoadMagusfile_topLevelLog(t *testing.T) {
	const src = `import "magus"; magus.info("top-level");`
	g := LoadMagusfile(context.Background(), src)
	require.True(t, g.OK, "a top-level log must not fail the load: %+v", g.Diag)
}

// TestRun_magusRunEmptyAndNonList covers firstListStr's guard branches: an empty
// argv and a non-list argument both trace no run op.
func TestRun_magusRunEmptyArgv(t *testing.T) {
	const src = `
import "magus";
export fun release(ctx: magus\Context, args: [str]) > void { magus.run([]); }
`
	r := Run(context.Background(), src, "release", nil)
	require.True(t, r.OK, "dry-run failed: %+v", r.Diag)
	assert.Empty(t, r.Trace, "an empty argv traces no run op")
}

// TestRun_magusRunWithCharmSuffix: magus.run keeps the :charm suffix on the
// traced invocation (splitTargetRef path with a colon and charms).
func TestRun_magusRunWithCharmSuffix(t *testing.T) {
	const src = `
import "magus";
export fun image_build(ctx: magus\Context, args: [str]) > void {}
export fun release(ctx: magus\Context, args: [str]) > void { magus.run(["image-build:cd,fast"]); }
`
	r := Run(context.Background(), src, "release", nil)
	require.True(t, r.OK, "dry-run failed: %+v", r.Diag)
	require.Len(t, r.Trace, 1)
	assert.Equal(t, "run", r.Trace[0].Kind)
	assert.Equal(t, "image-build:cd,fast", r.Trace[0].Name, "both charms survive on the suffix")
}

// TestRun_spellListTargets drives buildSpell's listTargets member (and strsToList)
// from a target body: calling it must not blow up and traces nothing itself.
func TestRun_spellListTargets(t *testing.T) {
	const src = `
import "magus/spell/go";
magus.project({"spells": [go]});
export fun show(ctx: magus\Context, args: [str]) > void { go.listTargets(); }
`
	r := Run(context.Background(), src, "show", nil)
	require.True(t, r.OK, "dry-run failed: %+v", r.Diag)
	assert.Empty(t, r.Trace, "listTargets is introspection, not a traced host op")
}

// TestRun_spellArgsDetailNoArgsKey: an op called with a map that lacks an "args"
// key renders an empty detail (spellArgsDetail's MapGet-miss branch).
func TestRun_spellArgsDetailNoArgsKey(t *testing.T) {
	const src = `
import "magus/spell/go";
magus.project({"spells": [go]});
export fun build(ctx: magus\Context, args: [str]) > void { go["go-build"]({"env": {"CGO_ENABLED": "0"}}); }
`
	r := Run(context.Background(), src, "build", nil)
	require.True(t, r.OK, "dry-run failed: %+v", r.Diag)
	require.Len(t, r.Trace, 1)
	assert.Equal(t, "go-build", r.Trace[0].Name)
	assert.Empty(t, r.Trace[0].Detail, "no args key renders empty op detail")
}

// twoOpSpell is a spell buffer with two command ops declared out of order, so
// probeSpell/sortSpellOps must sort them and renderCommand runs for each.
const twoOpSpell = `import "magus/target";
fun beta(t: Target) > Command { return Command{ bin = "b", args = ["go"] }; }
fun alpha(t: Target) > Command { return Command{ bin = "a", args = ["run"] }; }
export fun mgs_listTargets() > any { return {"beta": beta, "alpha": alpha}; }
`

// TestLoadSpell_sortedOps: a multi-op spell buffer lists its ops sorted by name,
// exercising sortSpellOps' comparator (which the single-op fixtures never do).
func TestLoadSpell_sortedOps(t *testing.T) {
	g := LoadMagusfile(context.Background(), twoOpSpell)
	require.True(t, g.OK, "load failed: %+v", g.Diag)
	assert.True(t, g.Spell)
	var keys []string
	for _, tg := range g.Targets {
		keys = append(keys, tg.Key)
	}
	assert.Equal(t, []string{"alpha", "beta"}, keys, "ops are sorted by name")
}

// TestEval_tracerSpellBuffer covers Eval's WithTracer spell-buffer branch: each
// discovered op becomes one command trace entry rendered top to bottom.
func TestEval_tracerSpellBuffer(t *testing.T) {
	r := Eval(context.Background(), twoOpSpell, WithTracer())
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	require.Len(t, r.Trace, 2)
	assert.Equal(t, "command", r.Trace[0].Kind)
	assert.Equal(t, "a run", r.Trace[0].Detail)
	assert.Equal(t, "b go", r.Trace[1].Detail)
}

// TestEval_tracerSpellWard covers Eval's WithTracer ward loop: a spell op that
// raises a kind-coherence ward appends a "ward" op after the command.
func TestEval_tracerSpellWard(t *testing.T) {
	src := tourFile(t, "10-wards.buzz")
	r := Eval(context.Background(), src, WithTracer())
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	var sawWard bool
	for _, op := range r.Trace {
		if op.Kind == "ward" {
			sawWard = true
			assert.Contains(t, op.Detail, "MGS5002", "the ward detail carries its code")
		}
	}
	assert.True(t, sawWard, "the detached service must surface a ward in the tracer trace; got %+v", r.Trace)
}

// TestRunSpell_unknownOp: running an op name that no handler declares is a
// diagnostic, not an empty plan.
func TestRunSpell_unknownOp(t *testing.T) {
	r := Run(context.Background(), twoOpSpell, "gamma", nil)
	require.False(t, r.OK, "an unknown spell op must fail")
	require.NotNil(t, r.Diag)
	assert.Contains(t, r.Diag.Msg, "unknown op: gamma")
}

// TestProbeSpell_nonFunHandler: a mgs_listTargets whose map value is not a
// function is skipped, so the op set is empty but the load still succeeds.
func TestProbeSpell_nonFunHandler(t *testing.T) {
	const src = `export fun mgs_listTargets() > any { return {"x": 5}; }`
	g := LoadMagusfile(context.Background(), src)
	require.True(t, g.OK, "load failed: %+v", g.Diag)
	assert.True(t, g.Spell, "mgs_listTargets marks it a spell buffer")
	assert.Empty(t, g.Targets, "a non-fun handler is skipped")
}

// TestProbeSpell_handlerReturnsNonMap: a handler returning a non-map value has no
// MapView, so the op is skipped rather than crashing the probe.
func TestProbeSpell_handlerReturnsNonMap(t *testing.T) {
	const src = `
fun bad(t: any) > any { return 5; }
export fun mgs_listTargets() > any { return {"bad": bad}; }
`
	g := LoadMagusfile(context.Background(), src)
	require.True(t, g.OK, "load failed: %+v", g.Diag)
	assert.True(t, g.Spell)
	assert.Empty(t, g.Targets, "a non-map handler return is skipped")
}

// TestDecodeSpellOp_serviceCommandNotMap covers decodeSpellOp's branch where a
// Service's `command` field is present but not itself a map: the op is still
// classified a service (with no decodable command) and lists.
func TestDecodeSpellOp_serviceCommandNotMap(t *testing.T) {
	const src = `
fun svc(t: any) > any { return {"command": 5}; }
export fun mgs_listTargets() > any { return {"svc": svc}; }
`
	g := LoadMagusfile(context.Background(), src)
	require.True(t, g.OK, "load failed: %+v", g.Diag)
	require.True(t, g.Spell)
	require.Len(t, g.Targets, 1)
	assert.Equal(t, "svc", g.Targets[0].Key)

	r := Run(context.Background(), src, "svc", nil)
	require.True(t, r.OK, "dry-run failed: %+v", r.Diag)
	require.NotEmpty(t, r.Trace)
	assert.Equal(t, "service", r.Trace[0].Kind, "a command-shaped-but-not-map field still reads as a service")
}

// TestRun_charmBranchElseViaCtx re-confirms the ctx.has_charm path branches on the
// active charm set - exercising traceHasCharm's true and false returns through the
// ctx form rather than the global.
func TestRun_charmBranchViaCtx(t *testing.T) {
	const src = `
import "magus/spell/docker";
magus.project({"spells": [docker]});
export fun image_build(ctx: magus\Context, args: [str]) > void {
    if (ctx.has_charm("cd")) { docker["docker-build"]({"args": ["--push"]}); }
    else { docker["docker-build"]({"args": ["--load"]}); }
}
`
	off := Run(context.Background(), src, "image-build", nil)
	require.True(t, off.OK, "plain: %+v", off.Diag)
	assert.Contains(t, traceDetail(off), "--load")

	on := Run(context.Background(), src, "image-build", []string{"cd"})
	require.True(t, on.OK, "cd: %+v", on.Diag)
	assert.Contains(t, traceDetail(on), "--push")
}
