package watch

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

func TestBuiltinIgnore_VCSMeta(t *testing.T) {
	for _, path := range []string{
		"/work/.git",
		"/work/node_modules",
		"/work/.magus",
	} {
		assert.True(t, BuiltinIgnore(path), "BuiltinIgnore(%q) should be true", path)
	}
	for _, path := range []string{
		"/work/src/main.go",
		"/work/README.md",
	} {
		assert.False(t, BuiltinIgnore(path), "BuiltinIgnore(%q) should be false", path)
	}
}

func TestCompose_AnyTrue(t *testing.T) {
	alwaysTrue := func(string) bool { return true }
	alwaysFalse := func(string) bool { return false }

	// OR semantics: true if any predicate returns true
	assert.True(t, Compose(alwaysTrue, alwaysFalse)("x"), "Compose(true,false): OR should return true")
	assert.True(t, Compose(alwaysTrue, alwaysTrue)("x"), "Compose(true,true): OR should return true")
	assert.False(t, Compose(alwaysFalse, alwaysFalse)("x"), "Compose(false,false): OR should return false")
}

func TestCompose_Empty(t *testing.T) {
	none := Compose()
	assert.False(t, none("anything"), "Compose() should return false for any input")
}

// A CHECKOUT UNDER A DOT-DIRECTORY IS STILL A CHECKOUT. An agent worktree sits under
// <repo>/.claude/worktrees and a scratch checkout under ~/.cache/magus/checkouts, so every
// absolute path in either carries a dot segment above the root. Judging those segments
// silenced the watcher in both: the job feed showed nothing, and CI's watch tests timed out
// once the gate checkout moved under XDG_CACHE_HOME.
func TestBuiltinIgnoreNeverJudgesAboveTheRoot(t *testing.T) {
	for _, root := range []string{
		"/repo/.claude/worktrees/feature",
		"/home/runner/.cache/magus/checkouts/merge-queue/1/gate-1/tmp/TestX/001",
	} {
		assert.False(t, BuiltinIgnore(root+"/internal/trail/trail.go"), "a file under %s", root)
		assert.False(t, BuiltinIgnore(root+"/internal"), "a directory under %s", root)
		assert.True(t, BuiltinIgnore(root+"/node_modules"), "a skip dir under %s", root)
		assert.True(t, BuiltinIgnore(root+"/.git"), "a dot dir under %s", root)
	}
}

// FuzzIgnorePatternsMatch probes the compiled-pattern matcher with
// arbitrary regex/glob/literal patterns and arbitrary paths. The
// invariant is "no panic across pattern types"; an invalid regex must
// be silently dropped (not crash the matcher).
func FuzzIgnorePatternsMatch(f *testing.F) {
	f.Add("glob", "**/scratch/*", "/repo/api/scratch/foo")
	f.Add("regex", `\.tmp$`, "/repo/api/main.tmp")
	f.Add("literal", "node_modules", "/repo/web/node_modules/foo")
	f.Add("regex", "(", "/repo/api/main.go") // invalid regex
	f.Add("glob", "[", "/repo/api/main.go")  // invalid glob
	f.Fuzz(func(t *testing.T, typ, pat, abs string) {
		if strings.IndexByte(typ+pat+abs, 0) >= 0 {
			t.Skip()
		}
		match := IgnorePatterns("/repo", []types.IgnorePattern{{Type: types.PatternType(typ), Pattern: pat}})
		_ = match(abs) // panic = test failure
	})
}

var builtinPaths = []string{
	"/repo/api/main.go",
	"/repo/.git/config",
	"/repo/node_modules/lodash/index.js",
	"/repo/api/target/debug/foo",
	"/repo/.magus/abc123",
	"/repo/magus-1234-abcd.sock",
	"/repo/api/.main.go.swp",
	"/repo/api/main_test.go~",
	"/repo/web/app.ts",
	"/repo/dist/bundle.js",
}

// BenchmarkBuiltinIgnore measures the per-event cost of the built-in ignore
// predicate. It runs on every filesystem event, so the constant factor matters.
func BenchmarkBuiltinIgnore(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range builtinPaths {
			_ = BuiltinIgnore(p)
		}
	}
}

// BenchmarkComposedIgnore measures the cost of the layered predicate used in
// production: BuiltinIgnore + a set of glob patterns, composed via Compose.
func BenchmarkComposedIgnore(b *testing.B) {
	const wsRoot = "/repo"
	patterns := []types.IgnorePattern{
		{Type: types.PatternGlob, Pattern: "**/scratch/**"},
		{Type: types.PatternGlob, Pattern: "**/testdata/**"},
		{Type: types.PatternRegex, Pattern: `\.tmp$`},
		{Type: types.PatternLiteral, Pattern: "vendor"},
	}
	ignore := Compose(
		BuiltinIgnore,
		IgnorePatterns(wsRoot, patterns),
	)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range builtinPaths {
			_ = ignore(p)
		}
	}
}

// BenchmarkOutputsIgnore measures the per-event cost of the output-glob
// predicate that guards against build→output-write→rebuild loops.
func BenchmarkOutputsIgnore(b *testing.B) {
	const wsRoot = "/repo"
	globs := make([]types.Glob, 20)
	for i := range globs {
		globs[i] = types.Glob{Pattern: fmt.Sprintf("svc%02d/dist/**", i)}
	}
	ignore := OutputsIgnore(wsRoot, globs)

	paths := []string{
		"/repo/svc00/dist/out.bin",
		"/repo/api/main.go",
		"/repo/svc19/dist/index.js",
		"/repo/web/app.ts",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range paths {
			_ = ignore(p)
		}
	}
}
