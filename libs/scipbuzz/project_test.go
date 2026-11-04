package scipbuzz

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type projectSuite struct {
	suite.Suite
	workspace string
}

func TestProjectSuite(t *testing.T) { suite.Run(t, new(projectSuite)) }

func (s *projectSuite) SetupTest() {
	s.workspace = s.T().TempDir()
	s.write("shared/lib.buzz", "namespace shared;\n\nexport fun lib() > int {\n    return 1;\n}\n\nfun hidden() > int {\n    return 2;\n}\n")
	s.write("app/main.buzz", "import \"../shared/lib\";\nimport \"side\";\nimport \"fs\";\n\nfun main(args: [str]) > void {\n    lib();\n}\n")
	s.write("app/side.buzz", "export final answer = 42;\n")
	s.write("app/broken.buzz", "fun ( {\n")
}

func (s *projectSuite) write(rel, content string) {
	path := filepath.Join(s.workspace, filepath.FromSlash(rel))
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o644))
}

func (s *projectSuite) files() (*Project, map[string]File) {
	p, err := NewProject(Options{ProjectRoot: filepath.Join(s.workspace, "app"), WorkspaceRoot: s.workspace})
	s.Require().NoError(err)
	files, err := p.Files(s.T().Context())
	s.Require().NoError(err)
	byPath := map[string]File{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	return p, byPath
}

func (s *projectSuite) TestFilesResolveImportsToWorkspaceFilesOnly() {
	_, files := s.files()
	s.Equal([]Import{{Path: "app/side.buzz", Document: "side.buzz"}, {Path: "shared/lib.buzz"}}, files["main.buzz"].Imports)
}

func (s *projectSuite) TestParseFailureIsTheFilesErrorNamingIt() {
	_, files := s.files()
	s.Require().Error(files["broken.buzz"].Err)
	s.Contains(files["broken.buzz"].Err.Error(), "app/broken.buzz: ")
	s.NoError(files["main.buzz"].Err)
}

func (s *projectSuite) TestExportsNameTheNamespaceAndExportedDeclarations() {
	p, _ := s.files()
	s.Equal([]string{`namespace shared`, "0 lib"}, p.Exports("shared/lib.buzz"))
}

func (s *projectSuite) TestDocumentOfAFileThatDidNotParseIsAnError() {
	p, _ := s.files()
	_, _, err := p.Document("broken.buzz")
	s.Error(err)
}
