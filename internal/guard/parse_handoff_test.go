package guard

import (
	"slices"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/stretchr/testify/suite"
)

type ScriptHandoffSuite struct {
	suite.Suite
}

func TestScriptHandoffSuite(t *testing.T) {
	suite.Run(t, new(ScriptHandoffSuite))
}

func (s *ScriptHandoffSuite) handoffs(command string) []handedScript {
	f, err := parseFile(command, DialectBash)
	s.Require().NoError(err)
	return scriptHandoffs(f, DialectBash)
}

func (s *ScriptHandoffSuite) runs(command, name string) bool {
	cmds, ok := ParseCommands(command)
	s.Require().True(ok)
	return slices.ContainsFunc(cmds, func(c hint.Invocation) bool { return c.Name == name })
}

func (s *ScriptHandoffSuite) TestAPipedScriptReachesTheLineRules() {
	s.True(s.runs("echo 'git push' | sh", "git"))
}

func (s *ScriptHandoffSuite) TestAHeredocScriptReachesTheLineRules() {
	s.True(s.runs("bash <<'EOF'\ngit push\nEOF", "git"))
}

func (s *ScriptHandoffSuite) TestAnAwkSystemCallReachesTheLineRules() {
	s.True(s.runs(`awk 'BEGIN { system("git push") }'`, "git"))
}

func (s *ScriptHandoffSuite) TestAShellGivenAFileReadsNoStdin() {
	s.Empty(s.handoffs("echo x | bash script.sh"))
}

func (s *ScriptHandoffSuite) TestXargsHandsNoStdinToItsShell() {
	s.Empty(s.handoffs("echo x | xargs sh"))
}

func (s *ScriptHandoffSuite) TestAPipeIntoAReaderIsNoScript() {
	s.Empty(s.handoffs("echo hi | cat"))
}

func (s *ScriptHandoffSuite) TestAnAwkAlternationIsNoPipe() {
	s.Empty(s.handoffs(`awk -F'|' '/a|b/ || $2 == "x|y" { print $1 }' f`))
}

func (s *ScriptHandoffSuite) TestASedSubstitutionIsNoCommand() {
	s.Empty(s.handoffs(`sed -e 's/e/E/g' -e '/^e/d' f`))
}

func (s *ScriptHandoffSuite) TestAVimForcedWriteIsNoCommand() {
	s.Empty(s.handoffs("vim -c 'w! out' -c q f"))
}

func (s *ScriptHandoffSuite) TestAPdoExecIsNoShellCall() {
	s.Empty(s.handoffs(`php -r '$db->exec($sql);'`))
}
