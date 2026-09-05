package dry

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/dry/gen/mocks"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEval_value(t *testing.T) {
	r := Eval(context.Background(), "return (1 + 2) * 10;")
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	assert.Equal(t, "30", r.Result)
}

func TestEval_capturesPrint(t *testing.T) {
	r := Eval(context.Background(), `import "std"; std.print("hello"); std.print("world");`)
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	assert.Equal(t, "hello\nworld\n", r.Output)
}

func TestEval_errorPosition(t *testing.T) {
	r := Eval(context.Background(), "return 1 +;")
	require.False(t, r.OK, "expected a parse error")
	require.NotNil(t, r.Diag)
	assert.NotZero(t, r.Diag.Line, "expected a positioned diag, got %+v", r.Diag)
}

const sampleMagusfile = `
import "magus";
import "magus/spell/go";

magus.project({
    "spells": [go],
    "outputs": ["bin/**"],
    "targets": {"regen-pgo": {"skip_cache": "test policy"}, "lint": {"slots": 4}},
});

export fun format(ctx: magus\Context, args: [str]) > void { go["go-fmt"](); }
export fun lint(ctx: magus\Context, args: [str]) > void { ctx.needs(format); go["go-vet"](); }
export fun build(ctx: magus\Context, args: [str]) > void { ctx.needs(format); go["go-build"](); }
export fun ci(ctx: magus\Context, args: [str]) > void { ctx.needs(lint, build); }
`

func TestLoadMagusfile_graph(t *testing.T) {
	g := LoadMagusfile(context.Background(), sampleMagusfile)
	require.True(t, g.OK, "load failed: %+v", g.Diag)
	require.Len(t, g.Projects, 1)
	assert.Equal(t, ".", g.Projects[0].Path)
	assert.Equal(t, []string{"regen-pgo"}, g.Projects[0].NoCache)
	assert.Equal(t, []string{"lint=4"}, g.Projects[0].Slots)
	assert.Equal(t, []string{"go"}, g.Projects[0].Spells)

	gotTargets := map[string]bool{}
	for _, tg := range g.Targets {
		gotTargets[tg.Key] = true
	}
	for _, want := range []string{"format", "lint", "build", "ci"} {
		assert.True(t, gotTargets[want], "missing target %q (got %v)", want, gotTargets)
	}

	assert.True(t, hasEdge(g.Edges, "ci", "lint"), "edges = %+v", g.Edges)
	assert.True(t, hasEdge(g.Edges, "ci", "build"), "edges = %+v", g.Edges)
	assert.True(t, hasEdge(g.Edges, "lint", "format"), "edges = %+v", g.Edges)
	assert.True(t, hasEdge(g.Edges, "build", "format"), "edges = %+v", g.Edges)
}

// TestLoadMagusfile_noLanguage pins dryKnownProjectOptionKeys against a key the real
// binding accepts. The two lists are hand-mirrored, and when no_language was added to
// internal/interp/bindings only, this path rejected the workspace's OWN evals/magusfile.buzz
// - so the Playground, magus-docs, and editor diagnostics all failed on a valid file while
// `magus run` was happy. A mismatch is invisible until someone uses the key.
func TestLoadMagusfile_noLanguage(t *testing.T) {
	g := LoadMagusfile(context.Background(), `
import "magus";
magus\project({
    "name": "harness",
    "no_language": "polyglot harness; no single language pack describes it",
    "targets": {},
});
export fun build(ctx: magus\Context, args: [str]) > void {}
`)
	require.True(t, g.OK, "dry path rejected no_language: %+v", g.Diag)
	require.Len(t, g.Projects, 1)
}

// TestLoadMagusfile_everyTargetPolicyKeyAccepted is the dry-run half of the pin on
// types.TargetPolicyKeys; see its twin over the engine,
// TestParseBuzzProjectOpts_EveryTargetPolicyKeyAccepted, for why the shape is a table
// read rather than a list of keys written out here.
//
// This is the half that was red: the hand-copied list on this side had drifted to three
// keys, so memory_mb, cache, drift and drift_reason failed the preview of a magusfile
// `magus run` loads happily.
func TestLoadMagusfile_everyTargetPolicyKeyAccepted(t *testing.T) {
	require.NotEmpty(t, types.TargetPolicyKeys())
	for _, key := range types.TargetPolicyKeys() {
		t.Run(key, func(t *testing.T) {
			g := LoadMagusfile(context.Background(), `
import "magus";
magus\project({
    "name": "pinned",
    "targets": {"build": {"`+key+`": "x"}},
});
export fun build(ctx: magus\Context, args: [str]) > void {}
`)
			if g.Diag != nil {
				assert.NotContains(t, g.Diag.Msg, "unknown option",
					"the dry host rejects %q, which types.TargetPolicyKeys declares recognized", key)
			}
		})
	}
}

// TestLoadMagusfile_everyToolBoundKeyAccepted is the dry-run half of the pin on
// types.ToolBoundKeys; its twin over the engine is
// TestParseBuzzProjectOpts_EveryToolBoundKeyAccepted.
func TestLoadMagusfile_everyToolBoundKeyAccepted(t *testing.T) {
	require.NotEmpty(t, types.ToolBoundKeys)
	for _, key := range types.ToolBoundKeys {
		t.Run(key, func(t *testing.T) {
			g := LoadMagusfile(context.Background(), `
import "magus";
magus\project({
    "name": "pinned",
    "tools": {"go": {"`+key+`": "1.21"}},
});
export fun build(ctx: magus\Context, args: [str]) > void {}
`)
			require.True(t, g.OK, "the dry host rejects %q, which types.ToolBoundKeys declares recognized: %+v", key, g.Diag)
		})
	}
}

// TestLoadMagusfile_unknownToolBoundKeyRejected is the half that was red: this path
// never walked `tools` at all, so a typo inside a tool entry stayed green in the
// Playground and the dry-run preview and then failed the real run.
//
// The permissive direction of the same divergence targets[] had: nothing was falsely
// rejected, but the preview blessed a magusfile the engine refuses to load.
func TestLoadMagusfile_unknownToolBoundKeyRejected(t *testing.T) {
	g := LoadMagusfile(context.Background(), `
import "magus";
magus\project({
    "name": "pinned",
    "tools": {"go": {"minn": "1.21"}},
});
export fun build(ctx: magus\Context, args: [str]) > void {}
`)
	require.False(t, g.OK, "the dry host accepted a tool bound the engine rejects")
	require.NotNil(t, g.Diag)
	assert.Contains(t, g.Diag.Msg, `tools["go"]: unknown option "minn"`)
	assert.Contains(t, g.Diag.Msg, "known options: below, min")
}

func TestRun_orderAndTrace(t *testing.T) {
	r := Run(context.Background(), sampleMagusfile, "ci", nil)
	require.True(t, r.OK, "dry-run failed: %+v", r.Diag)
	// format must precede lint and build; everything precedes ci.
	pos := map[string]int{}
	for i, k := range r.Order {
		pos[k] = i
	}
	assert.Less(t, pos["format"], pos["lint"], "bad order: %v", r.Order)
	assert.Less(t, pos["format"], pos["build"], "bad order: %v", r.Order)
	assert.Less(t, pos["lint"], pos["ci"], "bad order: %v", r.Order)
	assert.Less(t, pos["build"], pos["ci"], "bad order: %v", r.Order)
	// The trace must include the traced spell ops from the dependencies.
	ops := map[string]bool{}
	for _, op := range r.Trace {
		ops[op.Name] = true
	}
	for _, want := range []string{"go-fmt", "go-vet", "go-build"} {
		assert.True(t, ops[want], "trace missing op %q (got %v)", want, ops)
	}
}

func TestRun_charmBranch(t *testing.T) {
	const src = `
import "magus";
import "magus/spell/docker";
magus.project({"spells": [docker]});
export fun image_build(ctx: magus\Context, args: [str]) > void {
    if (ctx.hasCharm("cd")) { docker["docker-build"]({"args": ["--push"]}); }
    else { docker["docker-build"]({"args": ["--load"]}); }
}
`
	detail := func(r Result) string {
		var b strings.Builder
		for _, op := range r.Trace {
			b.WriteString(op.Detail)
			b.WriteByte(' ')
		}
		return b.String()
	}

	plain := Run(context.Background(), src, "image-build", nil)
	require.True(t, plain.OK, "plain: %+v", plain.Diag)
	assert.Contains(t, detail(plain), "--load", "no charm should take the else branch")
	assert.NotContains(t, detail(plain), "--push")

	cd := Run(context.Background(), src, "image-build", []string{"cd"})
	require.True(t, cd.OK, "cd: %+v", cd.Diag)
	assert.Contains(t, detail(cd), "--push", "cd charm should take the has_charm branch")
	assert.NotContains(t, detail(cd), "--load")
}

func TestLoadMagusfile_patternNeeds(t *testing.T) {
	const src = `
export fun proto_generate(ctx: magus\Context, args: [str]) > void {}
export fun mock_generate(ctx: magus\Context, args: [str]) > void {}
export fun generate(ctx: magus\Context, args: [str]) > void { ctx.needs(ctx.glob("*-generate")); }
export fun regen(ctx: magus\Context, args: [str]) > void { ctx.needs(ctx.glob("proto-*", "mock-*")); }
`
	g := LoadMagusfile(context.Background(), src)
	require.True(t, g.OK, "load failed: %+v", g.Diag)

	depsOf := func(from string) []string {
		var out []string
		for _, e := range g.Edges {
			if e.From == from {
				out = append(out, e.To)
			}
		}
		sort.Strings(out)
		return out
	}
	assert.Equal(t, []string{"mock-generate", "proto-generate"}, depsOf("generate"), "glob should match both -generate targets")
	assert.Equal(t, []string{"mock-generate", "proto-generate"}, depsOf("regen"), "the two globs should match both -generate targets")
}

func TestRun_magusRunInvocation(t *testing.T) {
	const src = `
import "magus";
export fun image_build(ctx: magus\Context, args: [str]) > void {}
export fun release(ctx: magus\Context, args: [str]) > void !> any { magus.run(["image-build:cd"]); }
`
	g := LoadMagusfile(context.Background(), src)
	require.True(t, g.OK, "load failed: %+v", g.Diag)
	for _, e := range g.Edges {
		assert.NotEqual(t, "release", e.From, "magus.run is imperative and must not create a static DAG edge")
	}

	r := Run(context.Background(), src, "release", nil)
	require.True(t, r.OK, "dry-run failed: %+v", r.Diag)
	require.Len(t, r.Trace, 1, "release should trace exactly the recursive invocation")
	assert.Equal(t, "run", r.Trace[0].Kind)
	assert.Equal(t, "image-build:cd", r.Trace[0].Name, "the traced invocation keeps the :charm suffix")
}

func TestRun_targetNameCasing(t *testing.T) {
	const src = `
export fun mock_generate(ctx: magus\Context, args: [str]) > void {}
export fun image_build(ctx: magus\Context, args: [str]) > void {}
`
	for _, name := range []string{"mock-generate", "mockGenerate", "mock_generate", "MOCK_GENERATE", "MockGenerate", "image-build", "imageBuild"} {
		r := Run(context.Background(), src, name, nil)
		assert.True(t, r.OK, "casing %q should resolve to a known target", name)
	}
	assert.False(t, Run(context.Background(), src, "nope", nil).OK, "a genuinely unknown name still fails")
}

func TestRun_unknownTarget(t *testing.T) {
	r := Run(context.Background(), sampleMagusfile, "nope", nil)
	require.False(t, r.OK, "expected an unknown-target diag")
	assert.NotNil(t, r.Diag, "expected an unknown-target diag")
}

func hasEdge(edges []Edge, from, to string) bool {
	for _, e := range edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

func TestEval_HostModules(t *testing.T) {
	cases := map[string]string{
		`import "strings"; return strings.camelCase("hello world");`: "helloWorld",
		`import "encoding/base64"; return base64.encode("hi");`:      "aGk=",
	}
	for src, want := range cases {
		r := Eval(context.Background(), src)
		if !r.OK {
			t.Errorf("%q: eval failed: %+v", src, r.Diag)
			continue
		}
		got := r.Result
		// Result strings may be wrapped in quotes; trim.
		if len(got) >= 2 && got[0] == '"' && got[len(got)-1] == '"' {
			got = got[1 : len(got)-1]
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", src, got, want)
		}
	}
}

// TestEval_tracerSpellOp is the load-bearing proof for the spell docs' Run button:
// a canonical spell example that wires a fork op into a target must, under WithTracer,
// produce the op's dry-run trace rather than executing (or failing on) anything. It
// mirrors the shape of every authored spells/examples/**/*.buzz.
func TestEval_tracerSpellOp(t *testing.T) {
	const src = `
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun build(ctx: magus\Context, args: [str]) > void { go["go-build"](); }
`
	r := Eval(context.Background(), src, WithTracer())
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	require.Len(t, r.Trace, 1, "the go-build op should trace exactly one host op")
	assert.Equal(t, "build", r.Trace[0].Target, "op attributes to the target whose body invoked it")
	assert.Equal(t, "spell", r.Trace[0].Kind)
	assert.Equal(t, "go-build", r.Trace[0].Name)
}

// TestEval_tracerMultiTarget checks the trace flattens every target's ops in
// discovery (sorted-key) order, so a multi-op example reads top to bottom.
func TestEval_tracerMultiTarget(t *testing.T) {
	const src = `
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun build(ctx: magus\Context, args: [str]) > void { go["go-build"](); }
export fun test(ctx: magus\Context, args: [str]) > void { go["go-test"](); }
`
	r := Eval(context.Background(), src, WithTracer())
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	require.Len(t, r.Trace, 2)
	// Targets are probed in sorted key order: build before test.
	assert.Equal(t, "go-build", r.Trace[0].Name)
	assert.Equal(t, "go-test", r.Trace[1].Name)
}

// TestEval_withCatalog proves the SpellCatalog seam: the built-in surface the tracer
// stubs comes from the injected catalog, not a hard-coded manifest. A mock catalog with
// one fake built-in makes that spell's op trace like a real built-in's. This is the
// mock-driven replacement for the old hand-written manifest + drift-gate test.
func TestEval_withCatalog(t *testing.T) {
	const src = `
import "magus";
import "magus/spell/acme";

magus.project({ "spells": [acme] });

export fun deploy(ctx: magus\Context, args: [str]) > void { acme["acme-ship"](); }
`
	cat := mocks.NewMockSpellCatalog(t)
	cat.EXPECT().BuiltinOps().Return(map[string][]string{"acme": {"acme-ship"}})

	r := Eval(context.Background(), src, WithCatalog(cat))
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	require.Len(t, r.Trace, 1, "the acme-ship op from the injected catalog should trace")
	assert.Equal(t, "acme-ship", r.Trace[0].Name)
}

// TestEval_tracerParseError surfaces a compile failure as a Diag instead of a
// bogus empty trace, so a broken example shows the error rather than passing.
func TestEval_tracerParseError(t *testing.T) {
	r := Eval(context.Background(), "export fun build(ctx: magus\\Context, args: [str]) > void { this is not buzz }", WithTracer())
	assert.False(t, r.OK)
	assert.NotNil(t, r.Diag)
}

// TestEvalAnnouncesUnrunTestBlocks pins the fix for a silent no-op that nearly
// shipped in the docs. A snippet of `test "..." { assert(...) }` evaluates here
// with OK=true and no output whether the assertions hold or not - Buzz test bodies
// run only under the test runner - so a Run button over one renders nothing while
// implying the assertions passed. Skipping them is correct; saying nothing is not.
func TestEvalAnnouncesUnrunTestBlocks(t *testing.T) {
	// The exact shape that fooled me: a deliberately FALSE assertion.
	wrong := "import \"std\";\nimport \"strings\";\n\ntest \"deliberately wrong\" {\n    std\\assert(strings\\kebabCase(\"go_build\") == \"WRONG\");\n}\n"
	r := Eval(context.Background(), wrong)
	require.True(t, r.OK, "a test block still evaluates cleanly; that is the trap")
	assert.Contains(t, r.Output, "not run here",
		"a snippet whose assertions never ran must say so")
	assert.Contains(t, r.Output, "magus buzz -t",
		"and must name the runner that does run them")

	// Plural reads correctly.
	two := "test \"a\" {}\ntest \"b\" {}\n"
	assert.Contains(t, Eval(context.Background(), two).Output, "2 test blocks")

	// A snippet with no test block gets no note - the common case stays silent.
	plain := "import \"strings\";\nimport \"std\";\nstd\\print(strings\\kebabCase(\"go_build\"));"
	out := Eval(context.Background(), plain).Output
	assert.NotContains(t, out, "not run here")
	assert.Contains(t, out, "go-build", "and still shows the real output")
}

// TestSpellExamplesParseAndRecord walks every spells/examples/**/*.buzz file and
// asserts two things the spell docs rely on:
//
//  1. It parses under ParseEmbedded (the mode magusfiles and the playground use),
//     catching a syntax typo before it ships in a docs page. Mirrors
//     std/examples_test.go.
//  2. It records at least one host op under the recording evaluator (Eval with
//     WithTracer). This is the load-bearing guarantee for the Run button: an example
//     must actually invoke its op inside a target, or the dry-run trace is empty and
//     the button shows nothing. Catches an example that wires a spell but forgets to
//     call it.
//
// It lives here rather than in spells/, where the example files are, because it never
// touches the spells package - it exercises this evaluator against them. In spells/ it
// had to sit in an external test package, since importing this package from `package
// spells` closes a cycle: dry imports spells.
func TestSpellExamplesParseAndRecord(t *testing.T) {
	root := filepath.Join("..", "..", "spells", "examples")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skip("no spells/examples/ directory yet")
	}

	count := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".buzz") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: read: %v", path, err)
			return nil
		}
		if _, err := buzz.ParseEmbedded(string(src)); err != nil {
			t.Errorf("%s: parse: %v", path, err)
			return nil
		}
		r := Eval(context.Background(), string(src), WithTracer())
		if !r.OK {
			t.Errorf("%s: recorder eval failed: %v", path, r.Diag)
			return nil
		}
		if len(r.Trace) == 0 {
			t.Errorf("%s: recorder captured no ops; the example must call its op inside a target", path)
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	t.Logf("checked %d spell example file(s)", count)
}
