package guard

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

// bash is a shell call of command by agent, or by the root session when agent is "".
func (s *UnboundWriteInJobCheckoutSuite) bash(agent, command string) verdict.Judgment {
	env := map[string]any{"session_id": "8f2c6a1e", "cwd": s.session, "hook_event_name": "PreToolUse",
		"tool_name": "Bash", "tool_input": map[string]any{"command": command}}
	if agent != "" {
		env["agent_id"], env["agent_type"] = agent, "general-purpose"
	}
	return Judge(s.testContext(), Dependencies{}, Request{Input: hookJSON(s.T(), env), Host: "claude-code"})
}

func (s *UnboundWriteInJobCheckoutSuite) TestUnboundSubagentCopyIntoAJobMadeCheckoutIsDenied() {
	got := withoutVolatile(s.bash("a1b2c3", "cp /tmp/staged.go "+filepath.Join(s.own, "std", "module.go")))
	got.Lease = ""
	s.Equal(verdict.Judgment{Decision: verdict.Deny, Rule: string(denyRuleLeaseWrite)}, got)
}

func (s *UnboundWriteInJobCheckoutSuite) TestShellDenyNamesTheJobAndJobExec() {
	v := s.bash("a1b2c3", "cp /tmp/staged.go "+filepath.Join(s.own, "std", "module.go"))
	s.Contains(v.Reason, "the checkout job w's fork made")
	s.Contains(v.Reason, "job exec w")
}

func (s *UnboundWriteInJobCheckoutSuite) TestUnboundSubagentRedirectIntoAJobMadeCheckoutIsDenied() {
	v := s.bash("a1b2c3", "printf 'package std\\n' > "+filepath.Join(s.own, "std", "module.go"))
	s.Equal(verdict.Deny, v.Decision, v.Reason)
}

func (s *UnboundWriteInJobCheckoutSuite) TestUnboundSubagentMoveIntoAJobMadeCheckoutIsDenied() {
	v := s.bash("a1b2c3", "mv /tmp/staged.go "+filepath.Join(s.own, "std"))
	s.Equal(verdict.Deny, v.Decision, v.Reason)
}

func (s *UnboundWriteInJobCheckoutSuite) TestUnboundSubagentEditingAnExistingFileInPlaceIsDenied() {
	target := filepath.Join(s.own, "std", "module.go")
	s.Require().NoError(os.WriteFile(target, []byte("package std\n"), 0o644))
	v := s.bash("a1b2c3", fmt.Sprintf("tee -a %s < /dev/null", target))
	s.Equal(verdict.Deny, v.Decision, v.Reason)
}

func (s *UnboundWriteInJobCheckoutSuite) TestUnboundSubagentReadingAJobMadeCheckoutPasses() {
	v := s.bash("a1b2c3", "cat "+filepath.Join(s.own, "magusfile.buzz"))
	s.NotEqual(verdict.Deny, v.Decision, v.Reason)
}

func (s *UnboundWriteInJobCheckoutSuite) TestUnboundSubagentCopyOutsideEveryCheckoutPasses() {
	v := s.bash("a1b2c3", "cp /tmp/staged.go "+filepath.Join(s.T().TempDir(), "notes.txt"))
	s.NotEqual(verdict.Deny, v.Decision, v.Reason)
}

func (s *UnboundWriteInJobCheckoutSuite) TestRootSessionCopyIntoAJobMadeCheckoutPasses() {
	v := s.bash("", "cp /tmp/staged.go "+filepath.Join(s.own, "std", "module.go"))
	s.NotEqual(verdict.Deny, v.Decision, v.Reason)
}
