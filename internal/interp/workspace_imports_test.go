package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type WorkspaceImportsSuite struct {
	suite.Suite
	root string
	ctx  context.Context
}

func TestWorkspaceImportsSuite(t *testing.T) { suite.Run(t, new(WorkspaceImportsSuite)) }

func (s *WorkspaceImportsSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.ctx = types.WithWorkspace(s.T().Context(), rootWorkspace{root: s.root})
}

func (s *WorkspaceImportsSuite) TestAnImportThroughAConnectedWorkspacePasses() {
	dir := filepath.Join(s.root, filepath.FromSlash(types.WorkspacesDir))
	s.Require().NoError(os.MkdirAll(dir, 0o755))
	s.Require().NoError(os.Symlink(s.T().TempDir(), filepath.Join(dir, "magus")))
	s.NoError(checkWorkspaceImports(s.ctx, "magusfile.buzz", `import ".magus/workspaces/magus/spells/golang/golangci" as g;`))
}

func (s *WorkspaceImportsSuite) TestAnImportThroughAMissingWorkspaceRefusesWithMGS1059() {
	err := checkWorkspaceImports(s.ctx, "magusfile.buzz", `import ".magus/workspaces/magus/spells/golang/golangci" as g;`)
	s.Require().ErrorIs(err, types.WorkspaceNotConnected)
	s.Contains(err.Error(), "magusfile.buzz imports")
	s.Contains(err.Error(), "{name: magus, path: ...}")
}

func (s *WorkspaceImportsSuite) TestOtherImportsAreNotChecked() {
	s.NoError(checkWorkspaceImports(s.ctx, "magusfile.buzz", "import \"magus\";\nimport \"./hack/lint\" as lint;\n"))
}
