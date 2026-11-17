package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

// CheckoutJudgeDepsSuite judges from a hook whose working directory is a magus
// source checkout, by a binary that is not that checkout's ./magus and was
// built from another contract.
type CheckoutJudgeDepsSuite struct {
	suite.Suite
	root, binary string
}

func TestCheckoutJudgeDepsSuite(t *testing.T) {
	suite.Run(t, new(CheckoutJudgeDepsSuite))
}

func (s *CheckoutJudgeDepsSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "go.mod"), []byte("module github.com/egladman/magus\n\ngo 1.25\n"), 0o644))
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "magus"), []byte("#!/bin/sh\n"), 0o755))
	lock := filepath.Join(s.root, "internal", "commands", "testdata", "api.lock")
	s.Require().NoError(os.MkdirAll(filepath.Dir(lock), 0o755))
	s.Require().NoError(os.WriteFile(lock, []byte("config another.key\n"), 0o644))
	s.binary = filepath.Join(s.T().TempDir(), "magus")
	s.Require().NoError(os.WriteFile(s.binary, []byte("#!/bin/sh\n"), 0o755))
}

func (s *CheckoutJudgeDepsSuite) TestAnUndelegatedForeignJudgeLoadsNothing() {
	deps := checkoutJudgeDeps(context.Background(), Dependencies{}, s.root, s.binary)
	s.Error(deps.LoadFailure)
}

func (s *CheckoutJudgeDepsSuite) TestADelegateKeepsItsRules() {
	ctx := context.WithValue(context.Background(), delegatedByKey{}, filepath.Join(s.root, "magus"))
	deps := checkoutJudgeDeps(ctx, Dependencies{}, s.root, s.binary)
	s.NoError(deps.LoadFailure)
}
