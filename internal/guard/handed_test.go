package guard

import "github.com/egladman/magus/types"

// reply is a stop whose last message is text.
func (s *StopRuleSuite) reply(text string, again bool) string {
	return s.stop("Stop", map[string]any{"last_assistant_message": text, "stop_hook_active": again})
}

func (s *StopRuleSuite) TestAReplyHandingOverADeniedCommandIsRefused() {
	v := s.judge(Dependencies{}, s.reply("Run this yourself:\n\n```bash\ngit add -A\n```\n", false))
	s.Contains(v.Reason, "this reply hands the person `git add -A`")
	v.Reason = ""
	s.Equal(types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleBriefCommand)}, v)
}

func (s *StopRuleSuite) TestASecondStopLetsAHandedCommandThrough() {
	v := s.judge(Dependencies{}, s.reply("```bash\ngit add -A\n```\n", true))
	s.NotEqual(decisionDeny, v.Decision)
}

func (s *StopRuleSuite) TestAHandedBlockShownToForbidItPasses() {
	v := s.judge(Dependencies{}, s.reply("Never run this:\n```bash\ngit add -A\n```\n", false))
	s.NotEqual(decisionDeny, v.Decision)
}

func (s *StopRuleSuite) TestAHandedAllowedCommandAndProsePass() {
	v := s.judge(Dependencies{}, s.reply("Done. `git add -A` was refused, so:\n\n```bash\ngit status\n```\n", false))
	s.NotEqual(decisionDeny, v.Decision)
}
