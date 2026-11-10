package guard

import (
	"slices"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/stretchr/testify/suite"
)

type HandedScriptSuite struct {
	suite.Suite
}

func TestHandedScriptSuite(t *testing.T) {
	suite.Run(t, new(HandedScriptSuite))
}

func (s *HandedScriptSuite) handed(command string) []handedScript {
	f, err := parseFile(command, DialectBash)
	s.Require().NoError(err)
	return handedScripts(f, DialectBash)
}

func (s *HandedScriptSuite) runs(command, name string) bool {
	cmds, ok := ParseCommands(command)
	s.Require().True(ok)
	return slices.ContainsFunc(cmds, func(c hint.Invocation) bool { return c.Name == name })
}

func (s *HandedScriptSuite) TestAPipedScriptReachesTheLineRules() {
	s.True(s.runs("echo 'git push' | sh", "git"))
}

func (s *HandedScriptSuite) TestAHeredocScriptReachesTheLineRules() {
	s.True(s.runs("bash <<'EOF'\ngit push\nEOF", "git"))
}

func (s *HandedScriptSuite) TestAnAwkSystemCallReachesTheLineRules() {
	s.True(s.runs(`awk 'BEGIN { system("git push") }'`, "git"))
}

func (s *HandedScriptSuite) TestAShellGivenAFileReadsNoStdin() {
	s.Empty(s.handed("echo x | bash script.sh"))
}

func (s *HandedScriptSuite) TestXargsHandsNoStdinToItsShell() {
	s.Empty(s.handed("echo x | xargs sh"))
}

func (s *HandedScriptSuite) TestAPipeIntoAReaderIsNoScript() {
	s.Empty(s.handed("echo hi | cat"))
}

func (s *HandedScriptSuite) TestAnAwkAlternationIsNoPipe() {
	s.Empty(s.handed(`awk -F'|' '/a|b/ || $2 == "x|y" { print $1 }' f`))
}

func (s *HandedScriptSuite) TestASedSubstitutionIsNoCommand() {
	s.Empty(s.handed(`sed -e 's/e/E/g' -e '/^e/d' f`))
}

func (s *HandedScriptSuite) TestAVimForcedWriteIsNoCommand() {
	s.Empty(s.handed("vim -c 'w! out' -c q f"))
}

func (s *HandedScriptSuite) TestAPdoExecIsNoShellCall() {
	s.Empty(s.handed(`php -r '$db->exec($sql);'`))
}
