package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/types"
)

// CopyWriteSuite judges a file copied into the workspace from scratch as the write it is.
type CopyWriteSuite struct {
	suite.Suite
	judge   func(line string) verdict.Judgment
	root    string
	scratch string
	seen    []types.WriteRequest
}

func TestCopyWriteSuite(t *testing.T) { suite.Run(t, new(CopyWriteSuite)) }

func (s *CopyWriteSuite) SetupTest() {
	ctx, _ := spawnFixture(s.T())
	s.root = hookLocation(ctx, Dependencies{}).workspace
	s.scratch = s.T().TempDir()
	s.seen = nil
	deps := Dependencies{WriteRule: func(_ context.Context, req types.WriteRequest, _ hint.Gate) (verdict.Verdict, error) {
		s.seen = append(s.seen, req)
		if strings.Contains(req.Content, "forbidden") {
			return verdict.Verdict{Decision: verdict.Deny, Reason: "that word is not allowed here"}, nil
		}
		return verdict.Verdict{Decision: verdict.Defer}, nil
	}}
	s.judge = func(line string) verdict.Judgment {
		return Judge(ctx, deps, Request{Input: line, Session: "copy", Host: "claude-code", Dir: s.root})
	}
}

// stage writes body to name in scratch and returns its path.
func (s *CopyWriteSuite) stage(name string, body []byte) string {
	p := filepath.Join(s.scratch, name)
	s.Require().NoError(os.WriteFile(p, body, 0o600))
	return p
}

// copy judges `verb src dst`.
func (s *CopyWriteSuite) copy(verb, src, dst string) verdict.Judgment {
	return s.judge(fmt.Sprintf("%s %s %s", verb, src, dst))
}

func (s *CopyWriteSuite) TestACopyFromScratchMeetsTheWriteRules() {
	src := s.stage("notes.md", []byte("a forbidden word\n"))

	v := s.copy("cp", src, "notes.md")
	s.Equal(verdict.Judgment{Decision: verdict.Deny, Rule: string(denyRuleCopyWrite)}, judgedAs(v), v.Context)
	s.Contains(v.Reason, "that word is not allowed here")
	s.Require().NotEmpty(s.seen)
	s.Equal(types.WriteRequest{Path: filepath.Join(s.root, "notes.md"), Content: "a forbidden word\n"},
		types.WriteRequest{Path: s.seen[0].Path, Content: s.seen[0].Content})
}

func (s *CopyWriteSuite) TestMoveAndInstallMeetTheWriteRulesToo() {
	src := s.stage("notes.md", []byte("a forbidden word\n"))
	s.Equal(verdict.Deny, s.copy("mv", src, "notes.md").Decision)
	s.Equal(verdict.Deny, s.copy("install -m 644", src, "notes.md").Decision)
}

func (s *CopyWriteSuite) TestACopyIntoADirectoryLandsUnderItsName() {
	src := s.stage("notes.md", []byte("an allowed word\n"))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "docs"), 0o755))

	s.copy("cp", src, "docs")
	s.Require().NotEmpty(s.seen)
	s.Equal(filepath.Join(s.root, "docs", "notes.md"), s.seen[0].Path)
}

func (s *CopyWriteSuite) TestAnAllowedCopyPasses() {
	src := s.stage("notes.md", []byte("an allowed word\n"))
	s.NotEqual(verdict.Deny, s.copy("cp", src, "notes.md").Decision)
}

func (s *CopyWriteSuite) TestASourceOverTheCapIsRefused() {
	src := s.stage("big.md", []byte(strings.Repeat("a", copyWriteCap+1)))

	v := s.copy("cp", src, "big.md")
	s.Equal(verdict.Judgment{Decision: verdict.Deny, Rule: string(denyRuleCopyWrite)}, judgedAs(v), v.Context)
	s.Contains(v.Reason, "more than the guard reads to judge a copy")
}

func (s *CopyWriteSuite) TestAnUnreadableSourceIsRefused() {
	if os.Geteuid() == 0 {
		s.T().Skip("root reads a file whatever its mode")
	}
	src := s.stage("locked.md", []byte("x\n"))
	s.Require().NoError(os.Chmod(src, 0o000))

	v := s.copy("cp", src, "locked.md")
	s.Equal(verdict.Judgment{Decision: verdict.Deny, Rule: string(denyRuleCopyWrite)}, judgedAs(v), v.Context)
	s.Contains(v.Reason, "cannot be read, so its bytes cannot be judged")
}

func (s *CopyWriteSuite) TestABinarySourceIsJudgedByPathAlone() {
	src := s.stage("tool", append([]byte("forbidden"), 0, 1, 2))

	s.NotEqual(verdict.Deny, s.copy("cp", src, "tool").Decision)
	s.Require().NotEmpty(s.seen)
	s.Empty(s.seen[0].Content)
}

func (s *CopyWriteSuite) TestASourceInsideTheWorkspaceIsNotReadAgain() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "old.md"), []byte("a forbidden word\n"), 0o600))

	s.copy("cp", "old.md", "new.md")
	for _, req := range s.seen {
		s.NotContains(req.Content, "forbidden")
	}
}

func (s *CopyWriteSuite) TestACopyOutOfTheWorkspaceIsNotJudged() {
	src := s.stage("notes.md", []byte("a forbidden word\n"))
	s.NotEqual(verdict.Deny, s.copy("cp", src, filepath.Join(s.T().TempDir(), "x.md")).Decision)
}
