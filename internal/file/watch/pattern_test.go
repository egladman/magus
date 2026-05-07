package watch

import (
	"path/filepath"
	"testing"
)

func TestParsePattern(t *testing.T) {
	cases := []struct {
		in      string
		want    IgnorePattern
		wantErr bool
	}{
		{
			// Bare values are no longer accepted; explicit type= required.
			in:      "**/scratch/*",
			wantErr: true,
		},
		{
			in:      "*.tmp",
			wantErr: true,
		},
		{
			in:   "type=glob,pattern=**/scratch/*",
			want: IgnorePattern{Type: PatternGlob, Pattern: "**/scratch/*"},
		},
		{
			in:   `type=regex,pattern=\.tmp$`,
			want: IgnorePattern{Type: PatternRegex, Pattern: `\.tmp$`},
		},
		{
			in:   "type=literal,pattern=bazel-out/[k8-fastbuild]",
			want: IgnorePattern{Type: PatternLiteral, Pattern: "bazel-out/[k8-fastbuild]"},
		},
		{
			// Regex with quantifier: comma must be escaped.
			in:   `type=regex,pattern=\.v\d{2\,4}\.bak$`,
			want: IgnorePattern{Type: PatternRegex, Pattern: `\.v\d{2,4}\.bak$`},
		},
		{
			// pattern= without type= is now an error.
			in:      "pattern=**/scratch/*",
			wantErr: true,
		},
		{
			in:      "type=bogus,pattern=foo",
			wantErr: true,
		},
		{
			in:      "type=regex,pattern=[unclosed",
			wantErr: true,
		},
		{
			// Unknown key is always an error.
			in:      "key=value",
			wantErr: true,
		},
		{
			in:      "",
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParsePattern(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ParsePattern(%q) = %+v, want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePattern(%q): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("ParsePattern(%q) = %+v, want %+v", c.in, got, c.want)
			}
		})
	}
}

func TestIgnorePatterns_Glob(t *testing.T) {
	root := t.TempDir()
	pred := IgnorePatterns(root, []IgnorePattern{
		{Type: PatternGlob, Pattern: "**/scratch/*"},
		{Type: PatternGlob, Pattern: "*.tmp"},
	})

	if !pred(filepath.Join(root, "scratch/a.txt")) {
		t.Error("expected **/scratch/* to match scratch/a.txt")
	}
	if !pred(filepath.Join(root, "deep/nested/scratch/b.txt")) {
		t.Error("expected **/scratch/* to match deep/nested/scratch/b.txt")
	}
	if !pred(filepath.Join(root, "x.tmp")) {
		t.Error("expected *.tmp to match x.tmp")
	}
	if pred(filepath.Join(root, "a.txt")) {
		t.Error("a.txt should not match")
	}
}

func TestIgnorePatterns_Regex(t *testing.T) {
	root := t.TempDir()
	pred := IgnorePatterns(root, []IgnorePattern{
		{Type: PatternRegex, Pattern: `\.generated\.go$`},
	})

	if !pred(filepath.Join(root, "api/foo.generated.go")) {
		t.Error("expected regex to match foo.generated.go")
	}
	if pred(filepath.Join(root, "api/foo.go")) {
		t.Error("regex should not match foo.go")
	}
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
	if pred(filepath.Join(root, "bazel-out/[k8-fastbuild]/a.o")) {
		t.Error("literal should match path segments, not concatenations with slashes")
	}

	// Re-test with a single-segment literal.
	pred2 := IgnorePatterns(root, []IgnorePattern{
		{Type: PatternLiteral, Pattern: "[k8-fastbuild]"},
	})
	if !pred2(filepath.Join(root, "bazel-out/[k8-fastbuild]/a.o")) {
		t.Error("expected literal segment match for [k8-fastbuild]")
	}
	if pred2(filepath.Join(root, "bazel-out/k8-fastbuild/a.o")) {
		t.Error("literal should not match without the brackets")
	}
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
		got := pred(filepath.Join(root, c.path))
		if got != c.want {
			t.Errorf("pred(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIgnorePatterns_Empty(t *testing.T) {
	pred := IgnorePatterns(t.TempDir(), nil)
	if pred("/anything") {
		t.Error("empty patterns should never match")
	}
}
