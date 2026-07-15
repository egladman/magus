package dry

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/spell"
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
import "magus/spell/go";

magus.project({
    "spells": [go],
    "outputs": ["bin/**"],
    "targets": {"regen-pgo": {"skip_cache": true}, "lint": {"slots": 4}},
});

export fun format(args: [str]) > void { go["go-fmt"](); }
export fun lint(args: [str]) > void { magus.needs(magus.target.literal("format")); go["go-vet"](); }
export fun build(args: [str]) > void { magus.needs(magus.target.literal("format")); go["go-build"](); }
export fun ci(args: [str]) > void { magus.needs(magus.target.literal("lint"), magus.target.literal("build")); }
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
import "magus/spell/docker";
magus.project({"spells": [docker]});
export fun image_build(args: [str]) > void {
    if (magus.has_charm("cd")) { docker["docker-build"]({"args": ["--push"]}); }
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
export fun proto_generate(args: [str]) > void {}
export fun mock_generate(args: [str]) > void {}
export fun generate(args: [str]) > void { magus.needs(magus.target.glob("*-generate")); }
export fun regen(args: [str]) > void { magus.needs(magus.target.regex("^(proto|mock)-generate$")); }
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
	assert.Equal(t, []string{"mock-generate", "proto-generate"}, depsOf("regen"), "regex should match both -generate targets")
}

func TestRun_magusRunInvocation(t *testing.T) {
	const src = `
export fun image_build(args: [str]) > void {}
export fun release(args: [str]) > void { magus.run(["image-build:cd"]); }
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
export fun mock_generate(args: [str]) > void {}
export fun image_build(args: [str]) > void {}
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

// TestManifestMatchesBuiltins gates the hand-written spell manifest against the
// real built-in registry: every spell and op the playground claims must exist.
// (Host-only: the spell package's embedded bytecode never enters the wasm build.)
func TestManifestMatchesBuiltins(t *testing.T) {
	builtins := spell.Builtins()
	for name, ops := range builtinSpellOps {
		spec, ok := builtins[name]
		if !assert.True(t, ok, "manifest spell %q is not a built-in", name) {
			continue
		}
		for _, op := range ops {
			_, ok := spec.Ops[op]
			assert.True(t, ok, "manifest op %q.%q is not a real target", name, op)
		}
	}
}

func hasEdge(edges []Edge, from, to string) bool {
	for _, e := range edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}
