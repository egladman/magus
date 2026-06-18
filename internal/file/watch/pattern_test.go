package watch

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePattern(t *testing.T) {
	wantOK := func(t *testing.T, in string, want IgnorePattern) {
		got, err := ParsePattern(in)
		require.NoError(t, err, "ParsePattern(%q)", in)
		assert.Equal(t, want, got)
	}
	wantErr := func(t *testing.T, in string) {
		got, err := ParsePattern(in)
		assert.Error(t, err, "ParsePattern(%q) = %+v, want error", in, got)
	}

	// Bare values are no longer accepted; explicit type= required.
	t.Run("bare glob rejected", func(t *testing.T) { wantErr(t, "**/scratch/*") })
	t.Run("bare ext rejected", func(t *testing.T) { wantErr(t, "*.tmp") })

	t.Run("glob", func(t *testing.T) {
		wantOK(t, "type=glob,pattern=**/scratch/*", IgnorePattern{Type: PatternGlob, Pattern: "**/scratch/*"})
	})
	t.Run("regex", func(t *testing.T) {
		wantOK(t, `type=regex,pattern=\.tmp$`, IgnorePattern{Type: PatternRegex, Pattern: `\.tmp$`})
	})
	t.Run("literal", func(t *testing.T) {
		wantOK(t, "type=literal,pattern=bazel-out/[k8-fastbuild]", IgnorePattern{Type: PatternLiteral, Pattern: "bazel-out/[k8-fastbuild]"})
	})
	t.Run("regex with escaped comma", func(t *testing.T) {
		// Regex with quantifier: comma must be escaped.
		wantOK(t, `type=regex,pattern=\.v\d{2\,4}\.bak$`, IgnorePattern{Type: PatternRegex, Pattern: `\.v\d{2,4}\.bak$`})
	})

	// pattern= without type= is now an error.
	t.Run("pattern without type rejected", func(t *testing.T) { wantErr(t, "pattern=**/scratch/*") })
	t.Run("bogus type rejected", func(t *testing.T) { wantErr(t, "type=bogus,pattern=foo") })
	t.Run("invalid regex rejected", func(t *testing.T) { wantErr(t, "type=regex,pattern=[unclosed") })
	// Unknown key is always an error.
	t.Run("unknown key rejected", func(t *testing.T) { wantErr(t, "key=value") })
	t.Run("empty rejected", func(t *testing.T) { wantErr(t, "") })
}

func TestIgnorePatterns_Glob(t *testing.T) {
	root := t.TempDir()
	pred := IgnorePatterns(root, []IgnorePattern{
		{Type: PatternGlob, Pattern: "**/scratch/*"},
		{Type: PatternGlob, Pattern: "*.tmp"},
	})

	assert.True(t, pred(filepath.Join(root, "scratch/a.txt")), "**/scratch/* should match scratch/a.txt")
	assert.True(t, pred(filepath.Join(root, "deep/nested/scratch/b.txt")), "**/scratch/* should match deep/nested/scratch/b.txt")
	assert.True(t, pred(filepath.Join(root, "x.tmp")), "*.tmp should match x.tmp")
	assert.False(t, pred(filepath.Join(root, "a.txt")), "a.txt should not match")
}

func TestIgnorePatterns_Regex(t *testing.T) {
	root := t.TempDir()
	pred := IgnorePatterns(root, []IgnorePattern{
		{Type: PatternRegex, Pattern: `\.generated\.go$`},
	})

	assert.True(t, pred(filepath.Join(root, "api/foo.generated.go")), "regex should match foo.generated.go")
	assert.False(t, pred(filepath.Join(root, "api/foo.go")), "regex should not match foo.go")
}

func TestIgnorePatterns_Literal(t *testing.T) {
	root := t.TempDir()
	pred := IgnorePatterns(root, []IgnorePattern{
		{Type: PatternLiteral, Pattern: "bazel-out/[k8-fastbuild]"},
	})

	// Literal matches a path SEGMENT, not a substring. The literal
	// "bazel-out/[k8-fastbuild]" contains a slash, so it would never
	// match a single segment. Use this case to verify the
	// segment-only semantic: literal does not match the substring.
	assert.False(t, pred(filepath.Join(root, "bazel-out/[k8-fastbuild]/a.o")), "literal should match path segments, not concatenations with slashes")

	// Re-test with a single-segment literal.
	pred2 := IgnorePatterns(root, []IgnorePattern{
		{Type: PatternLiteral, Pattern: "[k8-fastbuild]"},
	})
	assert.True(t, pred2(filepath.Join(root, "bazel-out/[k8-fastbuild]/a.o")), "expected literal segment match for [k8-fastbuild]")
	assert.False(t, pred2(filepath.Join(root, "bazel-out/k8-fastbuild/a.o")), "literal should not match without the brackets")
}

func TestIgnorePatterns_LiteralGitignoreSemantics(t *testing.T) {
	// gitignore: bare name matches at any depth as a path segment.
	root := t.TempDir()
	pred := IgnorePatterns(root, []IgnorePattern{
		{Type: PatternLiteral, Pattern: "node_modules"},
	})

	cases := []struct {
		path string
		want bool
	}{
		{"node_modules/foo.js", true},
		{"web/studio/node_modules/foo.js", true},
		{"deep/nested/node_modules/inner.js", true},
		{"my_node_modules/foo.js", false},
		{"node_modules_backup/foo.js", false},
		{"src/foo.js", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, pred(filepath.Join(root, c.path)), "pred(%q)", c.path)
	}
}

func TestIgnorePatterns_Empty(t *testing.T) {
	pred := IgnorePatterns(t.TempDir(), nil)
	assert.False(t, pred("/anything"), "empty patterns should never match")
}
