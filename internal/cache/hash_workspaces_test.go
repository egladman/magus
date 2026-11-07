package cache

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type HashWorkspacesSuite struct {
	suite.Suite
	root string
}

func TestHashWorkspacesSuite(t *testing.T) {
	suite.Run(t, new(HashWorkspacesSuite))
}

func (s *HashWorkspacesSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.write(s.root, "own/main.go", "package own")
	s.write(s.root, "own/README.md", "# own")
}

func (s *HashWorkspacesSuite) write(root, rel, body string) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	s.Require().NoError(os.MkdirAll(filepath.Dir(p), 0o755))
	s.Require().NoError(os.WriteFile(p, []byte(body), 0o644))
}

func (s *HashWorkspacesSuite) walked() []string {
	out, err := expandSources(types.MustParseGlobs("**/*.go", "**/*.md"), s.root, nil, nil)
	s.Require().NoError(err)
	var rels []string
	for _, ra := range out {
		rels = append(rels, ra.rel)
	}
	return rels
}

func (s *HashWorkspacesSuite) TestLinkedNeighbourIsNeverWalked() {
	if runtime.GOOS == "windows" {
		s.T().Skip("symlink creation is restricted on Windows")
	}
	neighbour := s.T().TempDir()
	s.write(neighbour, "buzzed_editor/editor.go", "package buzzededitor")
	s.write(neighbour, "docs/buzzed-guide.md", "# buzzed guide")
	link := filepath.Join(s.root, ".magus", "workspaces", "buzzed")
	s.Require().NoError(os.MkdirAll(filepath.Dir(link), 0o755))
	s.Require().NoError(os.Symlink(neighbour, link))

	s.Equal([]string{"own/README.md", "own/main.go"}, s.walked())
}

func (s *HashWorkspacesSuite) TestNeighbourFilesUnderTheDirectoryAreNeverWalked() {
	s.write(s.root, ".magus/workspaces/buzzed/buzzed_editor/editor.go", "package buzzededitor")
	s.write(s.root, ".magus/workspaces/buzzed/docs/buzzed-guide.md", "# buzzed guide")

	for _, rel := range s.walked() {
		s.False(strings.HasPrefix(rel, ".magus/"), "walked %s", rel)
	}
	s.Equal([]string{"own/README.md", "own/main.go"}, s.walked())
}
