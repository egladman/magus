//go:build !wasm

package std

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// rebuildingWorkspace builds its knowledge store from in once per
// evaluation, as the real workspace reads its warm graph once.
type rebuildingWorkspace struct {
	types.WorkspaceRepository
	cacheDir string
	in       knowledge.Inputs
	gaps     []types.KnowledgeSymbolGap
	probed   bool
	freshen  error
	calls    int
	scopes   [][]string
	// indexed is what the next freshen writes into in.Symbols, as a
	// rebuilt index does.
	indexed map[string][]types.KnowledgeSymbol
}

func (f *rebuildingWorkspace) FreshenSymbolIndexes(_ context.Context, languages []string) error {
	f.calls++
	f.scopes = append(f.scopes, languages)
	if f.indexed != nil {
		f.in.Symbols = f.indexed
	}
	return f.freshen
}

func (f *rebuildingWorkspace) KnowledgeGraph(ctx context.Context, _ types.GraphRequest) (*knowledge.Graph, error) {
	v, err := types.EvalMemoFromContext(ctx).Do("rebuilding-workspace", func() (any, error) {
		return knowledge.Build(ctx, f.cacheDir, knowledge.BuildOptions{}, f.in, nil)
	})
	if err != nil {
		return nil, err
	}
	g, _ := v.(*knowledge.Graph)
	return g, nil
}

func (f *rebuildingWorkspace) SymbolGaps(context.Context) ([]types.KnowledgeSymbolGap, bool) {
	return f.gaps, f.probed
}

func (f *rebuildingWorkspace) CacheDir() string { return f.cacheDir }

func indexWorkspace(t testing.TB) *rebuildingWorkspace {
	return &rebuildingWorkspace{
		cacheDir: filepath.Join(t.TempDir(), ".magus"),
		in: knowledge.Inputs{Graph: types.TargetGraphOutput{Projects: []types.TargetGraphProject{
			{Path: "pkg/a", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build"}}},
		}}},
		probed: true,
	}
}

func TestMagusSymbolIndexDigest(t *testing.T) {
	t.Run("freshens indexes before reading the digest", func(t *testing.T) {
		ws := indexWorkspace(t)
		_, err := MagusSymbolIndexDigest(types.WithWorkspace(t.Context(), ws), nil)
		require.NoError(t, err)
		assert.Equal(t, 1, ws.calls)
	})

	t.Run("does not read a digest when indexes cannot be freshened", func(t *testing.T) {
		ws := indexWorkspace(t)
		ws.freshen = assert.AnError
		_, err := MagusSymbolIndexDigest(types.WithWorkspace(t.Context(), ws), nil)
		require.ErrorIs(t, err, assert.AnError)
		assert.Equal(t, 1, ws.calls)
	})

	t.Run("no index is not indexed", func(t *testing.T) {
		got, err := MagusSymbolIndexDigest(types.WithWorkspace(t.Context(), indexWorkspace(t)), nil)
		require.NoError(t, err)
		assert.Equal(t, types.SymbolIndexDigest{Projects: []string{}}, got)
	})

	t.Run("reads the store the graph build just wrote, with the gaps", func(t *testing.T) {
		ws := indexWorkspace(t)
		ws.in.Symbols = map[string][]types.KnowledgeSymbol{
			"pkg/a": {{Key: "example.com/a Foo#", Label: "Foo", Source: "pkg/a/a.go:1", Defs: []string{"pkg/a/a.go"}}},
		}
		ws.gaps = []types.KnowledgeSymbolGap{{Project: types.NewProjectRef("console", ""), State: types.SymbolIndexNotBuilt}}
		ctx := types.WithWorkspace(t.Context(), ws)

		got, err := MagusSymbolIndexDigest(ctx, nil)
		require.NoError(t, err)
		want, err := knowledge.NewStore(ws.cacheDir, true, 0, nil, nil).SymbolIndexDigest()
		require.NoError(t, err)
		want.Gaps = ws.gaps
		assert.Equal(t, want, got)
		assert.True(t, got.Indexed)
		assert.Equal(t, []string{"pkg/a"}, got.Projects)

		ws.in.Symbols["pkg/a"][0].Label = "Renamed"
		moved, err := MagusSymbolIndexDigest(ctx, nil)
		require.NoError(t, err)
		assert.NotEqual(t, got.Digest, moved.Digest, "an index change reaches the digest without a separate graph build")
	})

	t.Run("an unrunnable gap probe raises", func(t *testing.T) {
		ws := indexWorkspace(t)
		ws.probed = false
		_, err := MagusSymbolIndexDigest(types.WithWorkspace(t.Context(), ws), nil)
		require.ErrorContains(t, err, "could not probe which declared symbol indexes are missing")
	})

	t.Run("outside a workspace raises", func(t *testing.T) {
		_, err := MagusSymbolIndexDigest(t.Context(), nil)
		require.ErrorContains(t, err, "magus\\graph: no workspace on the context")
	})
}

type symbolIndexLanguageSuite struct {
	suite.Suite
}

func TestSymbolIndexLanguageSuite(t *testing.T) {
	suite.Run(t, new(symbolIndexLanguageSuite))
}

func (s *symbolIndexLanguageSuite) TestDigestFreshensOnlyTheNamedLanguagesAndKeepsThem() {
	ws := indexWorkspace(s.T())
	got, err := MagusSymbolIndexDigest(types.WithWorkspace(s.T().Context(), ws), []string{"go"})
	s.Require().NoError(err)
	s.Equal([][]string{{"go"}}, ws.scopes)
	s.Equal([]string{"go"}, got.Languages)
}

func (s *symbolIndexLanguageSuite) TestGraphMethodHoldsTheIndexWithItsOpeningLanguages() {
	ws := indexWorkspace(s.T())
	ctx := types.WithWorkspace(s.T().Context(), ws)
	g, err := MagusGraphOpen(ctx, []string{"go", "typescript"})
	s.Require().NoError(err)
	s.Require().NoError(heldGraph(ctx, g, "precedents"))
	s.Equal([][]string{{"go", "typescript"}, {"go", "typescript"}}, ws.scopes)
}

type symbolIndexMemoSuite struct {
	suite.Suite
	ws *rebuildingWorkspace
}

func TestSymbolIndexMemoSuite(t *testing.T) {
	suite.Run(t, new(symbolIndexMemoSuite))
}

func (s *symbolIndexMemoSuite) SetupTest() {
	s.ws = indexWorkspace(s.T())
}

func (s *symbolIndexMemoSuite) evaluation() context.Context {
	return types.WithEvalMemo(types.WithWorkspace(s.T().Context(), s.ws))
}

func (s *symbolIndexMemoSuite) TestOneEvaluationFreshensOnceForEveryGraphRead() {
	ctx := s.evaluation()
	g, err := MagusGraphOpen(ctx, nil)
	s.Require().NoError(err)
	s.Require().NoError(heldGraph(ctx, g, "dir"))
	_, err = MagusGraphOpen(ctx, nil)
	s.Require().NoError(err)
	s.Equal(1, s.ws.calls)
}

func (s *symbolIndexMemoSuite) TestEachLanguagesListFreshensOnItsOwn() {
	ctx := s.evaluation()
	_, err := MagusSymbolIndexDigest(ctx, []string{"go"})
	s.Require().NoError(err)
	got, err := MagusSymbolIndexDigest(ctx, nil)
	s.Require().NoError(err)
	s.Equal([][]string{{"go"}, nil}, s.ws.scopes)
	s.Nil(got.Languages)
}

func (s *symbolIndexMemoSuite) TestAFailedFreshenIsTriedAgain() {
	ctx := s.evaluation()
	s.ws.freshen = assert.AnError
	_, err := MagusSymbolIndexDigest(ctx, nil)
	s.Require().ErrorIs(err, assert.AnError)
	s.ws.freshen = nil
	_, err = MagusSymbolIndexDigest(ctx, nil)
	s.Require().NoError(err)
	s.Equal(2, s.ws.calls)
}

func (s *symbolIndexMemoSuite) TestANewEvaluationFreshensAgain() {
	ctx := s.evaluation()
	_, err := MagusSymbolIndexDigest(ctx, nil)
	s.Require().NoError(err)
	_, err = MagusSymbolIndexDigest(types.WithEvalMemo(ctx), nil)
	s.Require().NoError(err)
	s.Equal(2, s.ws.calls)
}

type symbolIndexFreshenSuite struct {
	suite.Suite
	ws *rebuildingWorkspace
}

func TestSymbolIndexFreshenSuite(t *testing.T) {
	suite.Run(t, new(symbolIndexFreshenSuite))
}

func (s *symbolIndexFreshenSuite) SetupTest() {
	s.ws = indexWorkspace(s.T())
	s.ws.in.Symbols = map[string][]types.KnowledgeSymbol{
		"pkg/a": {{Key: "example.com/a Foo#", Label: "Foo", Source: "pkg/a/a.go:1", Defs: []string{"pkg/a/a.go"}}},
	}
}

func (s *symbolIndexFreshenSuite) TestTheMemoizedDigestIsTheStoresDigest() {
	plain, err := MagusSymbolIndexDigest(types.WithWorkspace(s.T().Context(), s.ws), nil)
	s.Require().NoError(err)
	memo, err := MagusSymbolIndexDigest(types.WithEvalMemo(types.WithWorkspace(s.T().Context(), s.ws)), nil)
	s.Require().NoError(err)
	stored, err := knowledge.NewStore(s.ws.cacheDir, true, 0, nil, nil).SymbolIndexDigest()
	s.Require().NoError(err)
	s.Require().True(stored.Indexed, "the fixture indexes pkg/a")
	s.Equal(stored, memo)
	s.Equal(plain, memo)
}

func (s *symbolIndexFreshenSuite) evaluation() context.Context {
	return types.WithEvalMemo(types.WithWorkspace(s.T().Context(), s.ws))
}

// indexB has the next freshen index pkg/b beside pkg/a.
func (s *symbolIndexFreshenSuite) indexB() {
	s.ws.indexed = map[string][]types.KnowledgeSymbol{
		"pkg/a": s.ws.in.Symbols["pkg/a"],
		"pkg/b": {{Key: "example.com/b Bar#", Label: "Bar", Source: "pkg/b/b.go:1", Defs: []string{"pkg/b/b.go"}}},
	}
}

func (s *symbolIndexFreshenSuite) read(ctx context.Context, req types.GraphRequest) {
	g, err := graphsFromContext(ctx, "dir")
	s.Require().NoError(err)
	_, err = g.KnowledgeGraph(ctx, req)
	s.Require().NoError(err)
}

func (s *symbolIndexFreshenSuite) TestASecondLanguagesListDigestsTheStoreItsFreshenMoved() {
	ctx := s.evaluation()
	first, err := MagusSymbolIndexDigest(ctx, []string{"go"})
	s.Require().NoError(err)
	s.indexB()
	second, err := MagusSymbolIndexDigest(ctx, nil)
	s.Require().NoError(err)
	s.Equal([]string{"pkg/a"}, first.Projects)
	s.Equal([]string{"pkg/a", "pkg/b"}, second.Projects)
}

func (s *symbolIndexFreshenSuite) TestADigestAfterAGraphReadDigestsTheStoreItsFreshenMoved() {
	ctx := s.evaluation()
	s.read(ctx, types.GraphRequest{})
	s.indexB()
	got, err := MagusSymbolIndexDigest(ctx, nil)
	s.Require().NoError(err)
	s.Equal([]string{"pkg/a", "pkg/b"}, got.Projects)
}

// graphHandleReads is a figure's shape: it opens magus\graph(), then
// holds the handle to its index on every member call.
func graphHandleReads(ctx context.Context, b *testing.B) {
	b.Helper()
	g, err := MagusGraphOpen(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	for range 20 {
		if err := heldGraph(ctx, g, "dir"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGraphHandleReadsWithoutMemo(b *testing.B) {
	ws := indexWorkspace(b)
	for b.Loop() {
		graphHandleReads(types.WithWorkspace(b.Context(), ws), b)
	}
}

func BenchmarkGraphHandleReadsInOneEvaluation(b *testing.B) {
	ws := indexWorkspace(b)
	for b.Loop() {
		graphHandleReads(types.WithEvalMemo(types.WithWorkspace(b.Context(), ws)), b)
	}
}
