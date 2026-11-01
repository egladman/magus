package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// deviationAuthorSuite pins who may add a ledger line and through which door.
type deviationAuthorSuite struct {
	suite.Suite
}

func TestDeviationAuthor(t *testing.T) {
	suite.Run(t, new(deviationAuthorSuite))
}

const deviationLine = `{"code":"R","subject":"a.proto: gone","reason":"renamed"}`

func (s *deviationAuthorSuite) TestAnEditAddingALineIsRefused() {
	f := writeFields{OldText: deviationLine, NewText: deviationLine + "\n" + `{"code":"S","subject":"b","reason":"r"}`}
	s.Contains(denyDeviationWrite("proto/buf-breaking.deviations.jsonl", f), "only a person adds a line")
}

func (s *deviationAuthorSuite) TestAnEditDeletingALinePasses() {
	s.Empty(denyDeviationWrite("x.deviations.jsonl", writeFields{OldText: deviationLine + "\n"}))
}

func (s *deviationAuthorSuite) TestARewordedLineCountsAsAdded() {
	f := writeFields{OldText: deviationLine, NewText: strings.Replace(deviationLine, "renamed", "moved", 1)}
	s.NotEmpty(denyDeviationWrite("x.deviations.jsonl", f))
}

func (s *deviationAuthorSuite) TestResolvingAConflictByKeepingBothSidesPasses() {
	f := writeFields{OldText: "<<<<<<< a\n" + deviationLine + "\n=======\nb\n>>>>>>> c\n", NewText: deviationLine + "\nb\n"}
	s.Empty(denyDeviationWrite("x.deviations.jsonl", f))
}

func (s *deviationAuthorSuite) TestAWholeFileWriteIsComparedWithTheDisk() {
	file := filepath.Join(s.T().TempDir(), "x.deviations.jsonl")
	s.Require().NoError(os.WriteFile(file, []byte(deviationLine+"\nb\n"), 0o644))
	s.Empty(denyDeviationWrite(file, writeFields{Content: deviationLine + "\n"}), "only removes")
	s.NotEmpty(denyDeviationWrite(file, writeFields{Content: deviationLine + "\nc\n"}), "adds c")
}

func (s *deviationAuthorSuite) TestAWriteThatShowsNoTextIsRefused() {
	s.NotEmpty(denyDeviationWrite("x.deviations.jsonl", writeFields{}))
}

func (s *deviationAuthorSuite) TestAnotherPathIsNotJudged() {
	s.Empty(denyDeviationWrite("deviations.jsonl.md", writeFields{NewText: "a"}))
}

func (s *deviationAuthorSuite) TestEveryShellWriteIsRefused() {
	for _, line := range []string{
		`echo x >> proto/a.deviations.jsonl`,
		`echo x > a.deviations.jsonl`,
		`printf x | tee -a a.deviations.jsonl`,
		`cp other a.deviations.jsonl`,
		`mv other a.deviations.jsonl`,
		`sh -c 'echo x >> a.deviations.jsonl'`,
	} {
		s.NotEmpty(denyDeviationCommand("", line, DialectBash), line)
	}
}

func (s *deviationAuthorSuite) TestReadingRemovingAndReplayingPass() {
	for _, line := range []string{
		`cat a.deviations.jsonl`,
		`echo a.deviations.jsonl`,
		`rm a.deviations.jsonl`,
		`git rm a.deviations.jsonl`,
		`git merge main`,
		`git revert abc123`,
		`git checkout main -- a.deviations.jsonl`,
	} {
		s.Empty(denyDeviationCommand("", line, DialectBash), line)
	}
}

func (s *deviationAuthorSuite) TestARemoveDoesNotExcuseAWriteOnTheSameLine() {
	s.NotEmpty(denyDeviationCommand("", `rm a.deviations.jsonl; echo x > a.deviations.jsonl`, DialectBash))
}

func (s *deviationAuthorSuite) ledgerPatch() (dir, file string) {
	dir = s.T().TempDir()
	file = filepath.Join(dir, "line.diff")
	s.Require().NoError(os.WriteFile(file, []byte(gitDiffTouching("proto/a.deviations.jsonl")), 0o644))
	return dir, file
}

func (s *deviationAuthorSuite) TestAnUnleasedPatchOfALedgerIsRefused() {
	dir, file := s.ledgerPatch()
	for _, line := range []string{
		"git apply " + file,
		"git apply line.diff",
		"git apply < line.diff",
		fmt.Sprintf("cat %s | git apply", file),
		"patch -p1 -i " + file,
		fmt.Sprintf("sh -c 'git apply %s'", file),
	} {
		s.Contains(denyDeviationCommand(dir, line, DialectBash), "a.deviations.jsonl", line)
	}
}

func (s *deviationAuthorSuite) TestAHeredocPatchOfALedgerIsRefused() {
	line := fmt.Sprintf("git apply <<'EOF'\n%sEOF", gitDiffTouching("a.deviations.jsonl"))
	s.NotEmpty(denyDeviationCommand("", line, DialectBash))
}

func (s *deviationAuthorSuite) TestAPatchNamingALedgerAsItsOutputIsRefused() {
	dir, file := s.ledgerPatch()
	s.NotEmpty(denyDeviationCommand(dir, "patch -o a.deviations.jsonl -i "+file, DialectBash))
}

func (s *deviationAuthorSuite) TestCheckingALedgerPatchReads() {
	dir, file := s.ledgerPatch()
	s.Empty(denyDeviationCommand(dir, "git apply --check "+file, DialectBash))
}

func (s *deviationAuthorSuite) TestAPatchOfAnotherFilePasses() {
	dir := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "other.diff"), []byte(gitDiffTouching("notes.md")), 0o644))
	s.Empty(denyDeviationCommand(dir, "git apply other.diff", DialectBash))
}

func (s *deviationAuthorSuite) TestAPipedPatchNamesNoLedger() {
	s.Empty(denyDeviationCommand("", "git diff abc def | git apply", DialectBash))
}

func (s *deviationAuthorSuite) TestAShellScriptPatchingALedgerIsRefused() {
	dir, file := s.ledgerPatch()
	script := filepath.Join(dir, "add.sh")
	s.Require().NoError(os.WriteFile(script, fmt.Appendf(nil, "#!/bin/sh\ngit apply %s\n", file), 0o755))
	s.NotEmpty(denyDeviationCommand(dir, "sh add.sh", DialectBash))
}

func (s *deviationAuthorSuite) TestAPatchReachesTheRuleWithNoLease() {
	_, file := s.ledgerPatch()
	v := judgeShellLine(s.T().Context(), Dependencies{}, location{}, "", "git apply "+file, DialectBash)
	s.Equal(denyRuleDeviationAuthor, v.Rule.Name)
}

func (s *deviationAuthorSuite) TestInlineBuzzWritesAreRefused() {
	for _, program := range []string{
		`fs\writeFile("a.deviations.jsonl", content: "x");`,
		`fs\appendFile("a.deviations.jsonl", content: "x");`,
		`fs\copyFile("other", dst: "a.deviations.jsonl");`,
	} {
		s.NotEmpty(denyDeviationCommand("", fmt.Sprintf("magus buzz -e '%s'", program), DialectBash), program)
	}
}

func (s *deviationAuthorSuite) TestBuzzReadingALedgerPasses() {
	line := `magus buzz -e 'fs\writeFile("report.txt", content: fs\readFile("a.deviations.jsonl"));'`
	s.Empty(denyDeviationCommand("", line, DialectBash))
}

func (s *deviationAuthorSuite) TestAScriptIsJudgedByItsContent() {
	dir := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "add.buzz"), []byte(`fs\appendFile("a.deviations.jsonl", content: "x");`), 0o644))
	s.NotEmpty(denyDeviationCommand(dir, `magus buzz add.buzz`, DialectBash))
}

func (s *deviationAuthorSuite) TestAShellLineReachesTheRuleWithNoLease() {
	v := judgeShellLine(s.T().Context(), Dependencies{}, location{}, "", `echo x >> a.deviations.jsonl`, DialectBash)
	s.Equal(denyRuleDeviationAuthor, v.Rule.Name)
	s.Contains(v.Deny, "only a person adds a line")
}

func (s *deviationAuthorSuite) TestAnEditReachesTheRuleWithNoLease() {
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"a.deviations.jsonl","old_string":"","new_string":"x"}}`
	got := Judge(s.T().Context(), testDependencies(), Request{Input: payload})
	got.Reason, got.Next = "", nil
	s.Equal(types.GuardJudgment{SchemaVersion: got.SchemaVersion, Decision: decisionDeny, Rule: string(denyRuleDeviationAuthor)}, got)
}

func (s *deviationAuthorSuite) TestTheRuleSaysItIsASeatbelt() {
	s.Contains(ruleDocDeviationAuthor.Why, "seatbelt")
	s.Contains(ruleDocDeviationAuthor.Why, "the root included")
}
