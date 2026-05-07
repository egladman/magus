package watch_test

import (
	"fmt"
	"testing"

	"github.com/egladman/magus/internal/file/watch"
)

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
			_ = watch.BuiltinIgnore(p)
		}
	}
}

// BenchmarkComposedIgnore measures the cost of the layered predicate used in
// production: BuiltinIgnore + a set of glob patterns, composed via Compose.
func BenchmarkComposedIgnore(b *testing.B) {
	const wsRoot = "/repo"
	patterns := []watch.IgnorePattern{
		{Type: watch.PatternGlob, Pattern: "**/scratch/**"},
		{Type: watch.PatternGlob, Pattern: "**/testdata/**"},
		{Type: watch.PatternRegex, Pattern: `\.tmp$`},
		{Type: watch.PatternLiteral, Pattern: "vendor"},
	}
	ignore := watch.Compose(
		watch.BuiltinIgnore,
		watch.IgnorePatterns(wsRoot, patterns),
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
	globs := make([]string, 20)
	for i := range globs {
		globs[i] = fmt.Sprintf("svc%02d/dist/**", i)
	}
	ignore := watch.OutputsIgnore(wsRoot, globs)

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
