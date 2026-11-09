package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/suite"
)

type PrintCheckoutSuite struct {
	suite.Suite
}

func TestPrintCheckoutSuite(t *testing.T) {
	suite.Run(t, new(PrintCheckoutSuite))
}

func (s *PrintCheckoutSuite) TestTheTextNamesThePathWhatRanAndTheRemoval() {
	var out bytes.Buffer
	printCheckout(&out, job.ReviewCheckoutRecord{
		Path: "/c/checkout-0123456789ab", Revision: "0123456789abcdef", VCS: "git",
		Bootstrap: job.ReviewBootstrap{Ran: true, Install: []string{".", "console"}, Harnesses: true, HookNote: job.NoCheckoutHook},
		Remove:    "magus vcs checkout --remove /c/checkout-0123456789ab",
	})
	s.Equal("checked out 0123456789ab at /c/checkout-0123456789ab (git)\n"+
		"bootstrap: only what this workspace declares, after the checkout:\n"+
		"  install: ., console\n"+
		"  harnesses: magus agent harness install\n"+
		"  hook: no checkout.bootstrap declared\n"+
		"remove: magus vcs checkout --remove /c/checkout-0123456789ab\n", out.String())
}

func (s *PrintCheckoutSuite) TestTheTextNamesTheHookThatRan() {
	var out bytes.Buffer
	printCheckout(&out, job.ReviewCheckoutRecord{
		Path: "/c/x", Revision: "abc", VCS: "git",
		Bootstrap: job.ReviewBootstrap{Ran: true, Install: []string{}, Harnesses: true, Hook: "hack/b.buzz"},
		Remove:    "magus vcs checkout --remove /c/x",
	})
	s.Equal("checked out abc at /c/x (git)\n"+
		"bootstrap: only what this workspace declares, after the checkout:\n"+
		"  install: no project declares an install target\n"+
		"  harnesses: magus agent harness install\n"+
		"  hook: hack/b.buzz\n"+
		"remove: magus vcs checkout --remove /c/x\n", out.String())
}

func (s *PrintCheckoutSuite) TestTheTextOfASkippedBootstrapSaysSo() {
	var out bytes.Buffer
	printCheckout(&out, job.ReviewCheckoutRecord{
		Path: "/c/x", Revision: "abc", VCS: "git",
		Bootstrap: job.ReviewBootstrap{Install: []string{}, HookNote: "--no-bootstrap: nothing ran in the checkout"},
		Remove:    "magus vcs checkout --remove /c/x",
	})
	s.Equal("checked out abc at /c/x (git)\n"+
		"bootstrap: --no-bootstrap: nothing ran in the checkout\n"+
		"remove: magus vcs checkout --remove /c/x\n", out.String())
}

type PreserveBeforeRemovalSuite struct {
	suite.Suite
}

func TestPreserveBeforeRemovalSuite(t *testing.T) { suite.Run(t, new(PreserveBeforeRemovalSuite)) }

func (s *PreserveBeforeRemovalSuite) TestRemovingACheckoutPreservesItFirst() {
	s.T().Setenv("XDG_STATE_HOME", s.T().TempDir())
	dir := testkit.NewRepository(s.T(), map[string]string{"a.txt": "one\n"}).Root()
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644))
	res, err := vcs.Resolve(s.T().Context(), dir, "", types.VCSOptions{})
	s.Require().NoError(err)

	var out bytes.Buffer
	s.Require().NoError(preserveBeforeRemoval(s.T().Context(), &out, dir, res))
	s.Contains(out.String(), "preserved the uncommitted work in")

	refs, err := exec.Command("git", "-C", dir, "for-each-ref", "refs/magus/preserved/").Output()
	s.Require().NoError(err)
	s.NotEmpty(strings.TrimSpace(string(refs)), "nothing was captured before the removal")

	out.Reset()
	s.Require().NoError(preserveBeforeRemoval(s.T().Context(), &out, dir, res))
	s.Contains(out.String(), "already preserved")
}
