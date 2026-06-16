package watch

import (
	"fmt"
	"testing"
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
			_ = BuiltinIgnore(p)
		}
	}
}

// BenchmarkComposedIgnore measures the cost of the layered predicate used in
// production: BuiltinIgnore + a set of glob patterns, composed via Compose.
func BenchmarkComposedIgnore(b *testing.B) {
	const wsRoot = "/repo"
	patterns := []IgnorePattern{
		{Type: PatternGlob, Pattern: "**/scratch/**"},
		{Type: PatternGlob, Pattern: "**/testdata/**"},
		{Type: PatternRegex, Pattern: `\.tmp$`},
		{Type: PatternLiteral, Pattern: "vendor"},
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
	globs := make([]string, 20)
	for i := range globs {
		globs[i] = fmt.Sprintf("svc%02d/dist/**", i)
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
