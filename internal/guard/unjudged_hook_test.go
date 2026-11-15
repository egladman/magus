package guard

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/ward"
)

type UnjudgedHookSuite struct {
	suite.Suite
	cause error
}

func TestUnjudgedHookSuite(t *testing.T) { suite.Run(t, new(UnjudgedHookSuite)) }

func (s *UnjudgedHookSuite) SetupTest() { s.cause = errors.New("config: magus.yaml: unknown key") }

func (s *UnjudgedHookSuite) write(root, name, content string) {
	s.Require().NoError(os.WriteFile(filepath.Join(root, name), []byte(content), 0o644))
}

func (s *UnjudgedHookSuite) sourceRoot(hook string) string {
	root := s.T().TempDir()
	s.write(root, "go.mod", fmt.Sprintf("module %s\n", ownModule))
	if hook != "" {
		s.write(root, "magus.yaml", fmt.Sprintf("checkout:\n  bootstrap: %s\n", hook))
	}
	return root
}

func (s *UnjudgedHookSuite) answer(root, event string) (Unjudged, string) {
	return UnjudgedHook(root, strings.NewReader(event), s.cause)
}

func unjudgedEvent(command string) string {
	return fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":%q}}`, command)
}

func (s *UnjudgedHookSuite) TestAConsumerRepairsByInstallingARelease() {
	root := s.T().TempDir()
	s.write(root, "magus.yaml", "required_version: \"0.4.3\"\n")

	s.Equal([]string{"magus self update --version v0.4.3"}, repairLines(root))
	answer, reason := s.answer(root, unjudgedEvent("magus self update --version v0.4.3"))
	s.Equal(unjudgedRepair, answer)
	s.Empty(reason)
	s.Equal(0, answer.ExitCode())
}

func (s *UnjudgedHookSuite) TestAWorkspaceWithNoRootGetsTheReleaseToo() {
	s.Equal([]string{"magus self update"}, repairLines(""))
}

func (s *UnjudgedHookSuite) TestACheckoutOfMagusRepairsFromSource() {
	lines := repairLines(s.sourceRoot("hack/dev/bootstrap-checkout.buzz"))
	s.Contains(lines, ward.BootstrapCommand)
	s.Contains(lines, "./magus buzz hack/dev/bootstrap-checkout.buzz -- --here --verify-ready")
	s.NotContains(lines, ward.RelinkAt(""), "no ./magus to relink")
	s.NotContains(lines, "magus self update")
}

func (s *UnjudgedHookSuite) TestACheckoutHoldingABinaryAlsoRelinks() {
	root := s.sourceRoot("")
	s.write(root, "magus", "")

	s.Contains(repairLines(root), ward.RelinkAt(""))
	s.Contains(repairLines(root), ward.RelinkAt(root))
}

func (s *UnjudgedHookSuite) TestAnOrdinaryCallIsRefusedNamingCauseAndRepair() {
	answer, reason := s.answer(s.sourceRoot(""), unjudgedEvent("touch x"))
	s.Equal(unjudgedRefused, answer)
	s.Equal(2, answer.ExitCode())
	s.Contains(reason, "magus guard could not judge this call, so it is refused: config: magus.yaml: unknown key")
	s.Contains(reason, fmt.Sprintf("`%s`", ward.BootstrapCommand))
}

func (s *UnjudgedHookSuite) TestTheRepairChainedToAnythingIsRefused() {
	answer, _ := s.answer(s.sourceRoot(""), unjudgedEvent(ward.BootstrapCommand+" && rm -rf x"))
	s.Equal(unjudgedRefused, answer)
}

func (s *UnjudgedHookSuite) TestAReadIsRefused() {
	answer, _ := s.answer("", `{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"magus self update"}}`)
	s.Equal(unjudgedRefused, answer)
}

func (s *UnjudgedHookSuite) TestABareLineIsJudgedAsTheCommand() {
	answer, _ := s.answer("", "  magus self update\n")
	s.Equal(unjudgedRepair, answer)
}

func (s *UnjudgedHookSuite) TestAnEmptyEventIsRefused() {
	answer, _ := s.answer("", "")
	s.Equal(unjudgedRefused, answer)
}

func (s *UnjudgedHookSuite) TestAnEventNeverClosedReadsAsEmpty() {
	r, w := io.Pipe()
	defer w.Close()
	s.Empty(readHookEvent(r, 10*time.Millisecond))
}

func (s *UnjudgedHookSuite) TestAStopEndsAndSaysWhy() {
	answer, reason := s.answer("", `{"hook_event_name":"Stop","stop_hook_active":false}`)
	s.Equal(unjudgedStop, answer)
	s.Equal(1, answer.ExitCode())
	s.Contains(reason, "magus guard could not judge this stop, so it ends unjudged")
	s.Contains(reason, "`magus self update`")
}
