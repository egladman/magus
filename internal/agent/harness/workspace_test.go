//go:build e2e

package harness

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit/e2e"
)

// workspaceSetupSuite builds each installed host's fixture and stops before
// the host runs, so it spends no model tokens.
type workspaceSetupSuite struct{ e2e.Suite }

func TestWorkspaceSetup(t *testing.T) { suite.Run(t, new(workspaceSetupSuite)) }

func (s *workspaceSetupSuite) builds(h *hostDriver) {
	s.RequireBinary(h.install)
	newWorkspace(s.T(), s.T(), h)
}

func (s *workspaceSetupSuite) TestClaudeCode() { s.builds(claudeDriver()) }
func (s *workspaceSetupSuite) TestCodex()      { s.builds(codexDriver()) }
func (s *workspaceSetupSuite) TestCursor()     { s.builds(cursorDriver()) }
func (s *workspaceSetupSuite) TestOpenCode()   { s.builds(opencodeDriver()) }
