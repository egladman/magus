package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func signatureGraph(t *testing.T, language, signature string) *Graph {
	t.Helper()
	return mergeAll([]Shard{assembleSymbols("pkg/a", []types.KnowledgeSymbol{{
		Key: "x Subject().", Label: "Subject", Source: "pkg/a/a.go:1", Defs: []string{"pkg/a/a.go"},
		Language: language, Signature: signature,
	}}, []types.TargetGraphProject{{Path: "pkg/a"}})})
}

func TestSignatureMatches(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, language, indexed, declared string
		match                             bool
	}{
		{"same text", "go", "func Resolve(ctx context.Context) error", "func Resolve(ctx context.Context) error", true},
		{"whitespace collapses", "go", "func Resolve(ctx context.Context) error", "func  Resolve(ctx  context.Context)\terror", true},
		{"renamed parameter", "go", "func Resolve(ctx context.Context, row types.Job) error", "func Resolve(c context.Context, r types.Job) error", true},
		{"grouped parameters", "go", "func Diff(root string, rev string) error", "func Diff(root, rev string) error", true},
		{"index renders the receiver bare", "go", "func (Goal).Resolve() Goal", "func (g Goal) Resolve() Goal", true},
		{"pointer receiver", "go", "func (Store).Update(id string) error", "func (s *Store) Update(id string) error", true},
		{"named results", "go", "func Read() (int, error)", "func Read() (n int, err error)", true},
		{"changed parameter type", "go", "func Resolve(ctx context.Context) error", "func Resolve(ctx context.Context, id string) error", false},
		{"changed result", "go", "func Resolve() error", "func Resolve() (string, error)", false},
		{"other receiver", "go", "func (Goal).Resolve() Goal", "func (Job) Resolve() Goal", false},
		{"function against method", "go", "func Resolve() Goal", "func (g Goal) Resolve() Goal", false},
		{"renamed", "go", "func Resolve() Goal", "func resolve() Goal", false},
		{"go type compares text", "go", "type Goal struct", "type Goal struct", true},
		{"go value compares text", "go", "const ExpectSignature GoalExpect", "const ExpectSignature string", false},
		{"buzz renamed parameter", "buzz", "fun build(ctx: magus\\Context, name: str) > void !> any", "fun build(c: magus\\Context, n: str) > void !> any", true},
		{"buzz changed result", "buzz", "fun build(name: str) > void", "fun build(name: str) > str", false},
		{"typescript compares text", "typescript", "resolve(a: string): void", "resolve(b: string): void", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := signatureGraph(t, tc.language, tc.indexed)
			indexed, match, ok := g.SignatureMatches("symbol:x Subject().", tc.declared)
			assert.True(t, ok)
			assert.Equal(t, tc.indexed, indexed)
			assert.Equal(t, tc.match, match)
		})
	}
}

func TestSignatureMatchesComparesNothingWithoutASignature(t *testing.T) {
	t.Parallel()

	g := signatureGraph(t, "go", "")
	indexed, match, ok := g.SignatureMatches("symbol:x Subject().", "func Subject()")
	assert.Equal(t, []any{"", false, false}, []any{indexed, match, ok}, "a node with no signature attr")

	indexed, match, ok = g.SignatureMatches("symbol:x Missing().", "func Missing()")
	assert.Equal(t, []any{"", false, false}, []any{indexed, match, ok}, "a node the graph does not hold")
}
