package std

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type diskGraphWorkspace struct {
	types.WorkspaceRepository
	root     string
	cacheDir string
}

func (w *diskGraphWorkspace) KnowledgeGraph(ctx context.Context, _ types.GraphRequest) (*knowledge.Graph, error) {
	return knowledge.Build(ctx, w.cacheDir, knowledge.BuildOptions{Root: w.root}, knowledge.Inputs{Root: w.root}, slog.Default())
}

func (w *diskGraphWorkspace) SymbolGaps(context.Context) ([]types.KnowledgeSymbolGap, bool) {
	return nil, true
}

func (w *diskGraphWorkspace) CacheDir() string { return w.cacheDir }

type GraphWorkspacesSuite struct {
	suite.Suite
	root      string
	neighbour string
}

func TestGraphWorkspacesSuite(t *testing.T) {
	suite.Run(t, new(GraphWorkspacesSuite))
}

func (s *GraphWorkspacesSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.neighbour = s.T().TempDir()
	s.write(s.root, "docs/own-guide.md", "# own guide\n")
	s.write(s.neighbour, "docs/buzzed-guide.md", "# buzzed guide\n")
	s.write(s.neighbour, "buzzed_editor/editor.go", "package buzzededitor\n")
}

func (s *GraphWorkspacesSuite) write(root, rel, body string) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	s.Require().NoError(os.MkdirAll(filepath.Dir(p), 0o755))
	s.Require().NoError(os.WriteFile(p, []byte(body), 0o644))
}

func (s *GraphWorkspacesSuite) query(q string) types.KnowledgeQueryOutput {
	ws := &diskGraphWorkspace{root: s.root, cacheDir: s.T().TempDir()}
	out, err := MagusQuery(types.WithWorkspace(s.T().Context(), ws), q, types.KnowledgeQueryOptions{})
	s.Require().NoError(err)
	return out
}

func (s *GraphWorkspacesSuite) ids(out types.KnowledgeQueryOutput) []string {
	var ids []string
	for _, m := range out.Matches {
		ids = append(ids, m.ID)
	}
	return ids
}

func (s *GraphWorkspacesSuite) link() {
	if runtime.GOOS == "windows" {
		s.T().Skip("symlink creation is restricted on Windows")
	}
	link := filepath.Join(s.root, ".magus", "workspaces", "buzzed")
	s.Require().NoError(os.MkdirAll(filepath.Dir(link), 0o755))
	s.Require().NoError(os.Symlink(s.neighbour, link))
}

func (s *GraphWorkspacesSuite) TestQueryAnswersTheSameWithTheNeighbourLinked() {
	before := s.query("kind=doc")
	s.Require().NotEmpty(s.ids(before), "the fixture must index its own doc")

	s.link()
	after := s.query("kind=doc")

	s.Equal(s.ids(before), s.ids(after))
	s.Equal(before.MatchCount, after.MatchCount)
}

func (s *GraphWorkspacesSuite) TestQueryNeverNamesTheNeighbour() {
	s.link()

	s.Zero(s.query("buzzed").MatchCount)
	s.Zero(s.query("buzzed_editor").MatchCount)
}
