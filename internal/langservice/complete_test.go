package langservice

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// labels flattens a completion slice to its labels for order-sensitive assertions.
func labels(cs []Completion) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Label
	}
	return out
}

// hasLabel reports whether any completion carries the given label, returning it.
func findLabel(cs []Completion, label string) (Completion, bool) {
	for _, c := range cs {
		if c.Label == label {
			return c, true
		}
	}
	return Completion{}, false
}

func TestComplete_ImportPath(t *testing.T) {
	// `import "f` -> module paths starting with f (fs, fmt, ...).
	src := `import "f`
	got := CompleteAt(src, len(src))
	require.NotEmpty(t, got, "expected import-path completions")
	for _, c := range got {
		assert.Equal(t, KindModule, c.Kind)
		assert.True(t, strings.HasPrefix(c.Label, "f"), "unexpected label %q", c.Label)
		assert.Equal(t, 1, c.Replace, "replaces the typed 'f'")
	}
	_, ok := findLabel(got, "fs")
	assert.True(t, ok, "fs should be offered, got %v", labels(got))
}

func TestComplete_ModuleMembers(t *testing.T) {
	src := "import \"fs\";\nfs."
	got := CompleteAt(src, len(src))
	require.NotEmpty(t, got, "expected fs members")
	for _, c := range got {
		assert.Contains(t, []CompletionKind{KindMethod, KindField}, c.Kind)
		assert.Equal(t, 0, c.Replace, "no partial typed after the dot")
	}
}

func TestComplete_ModuleMembers_Partial(t *testing.T) {
	// Direct module name (no import yet) still offers members, filtered by partial.
	src := "fs.gl"
	got := CompleteAt(src, len(src))
	require.NotEmpty(t, got)
	for _, c := range got {
		assert.True(t, strings.HasPrefix(c.Label, "gl"), "label %q not prefixed by partial", c.Label)
		assert.Equal(t, 2, c.Replace)
	}
	m, ok := findLabel(got, "glob")
	require.True(t, ok, "glob should be offered")
	assert.NotEmpty(t, m.Detail, "method should carry a signature detail")
}

func TestComplete_AliasedImport(t *testing.T) {
	src := "import \"fs\" as f;\nf."
	got := CompleteAt(src, len(src))
	require.NotEmpty(t, got, "aliased module members should resolve")
	_, ok := findLabel(got, "glob")
	assert.True(t, ok, "fs.glob should be offered under alias f")
}

func TestComplete_BuzzSchemeImport(t *testing.T) {
	// Upstream's `buzz:` package scheme binds the bare module name, so member
	// completion must resolve past the scheme.
	src := "import \"buzz:fs\";\nfs."
	got := CompleteAt(src, len(src))
	require.NotEmpty(t, got, "buzz: scheme import should resolve fs members")
	_, ok := findLabel(got, "glob")
	assert.True(t, ok, "fs.glob should be offered when imported via buzz:fs")
}

func TestComplete_Word_KeywordsModulesSymbols(t *testing.T) {
	src := "import \"fs\";\nexport fun build(ctx: magus\\Context, args: [str]) > void {}\nbu"
	got := CompleteAt(src, len(src))
	names := labels(got)
	// Its own top-level function is offered.
	assert.Contains(t, names, "build")
	for _, c := range got {
		assert.True(t, strings.HasPrefix(c.Label, "bu"), "label %q not prefixed", c.Label)
		assert.Equal(t, 2, c.Replace)
	}
}

func TestComplete_Word_EmptyPrefixIsQuiet(t *testing.T) {
	assert.Empty(t, CompleteAt("   ", 3), "no prefix should yield no word dump")
}

func TestComplete_OffsetClamped(t *testing.T) {
	assert.NotPanics(t, func() {
		CompleteAt("fs.", 999)
		CompleteAt("fs.", -5)
	})
}
