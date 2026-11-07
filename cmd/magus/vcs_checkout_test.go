package main

import (
	"bytes"
	"testing"

	"github.com/egladman/magus/internal/job"
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
