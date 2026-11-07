package guard

import (
	"fmt"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// LeaseDenialTextSuite pins rows 898 and 567: a refusal stays short on a
// wide row, and never serves a ./magus the checkout does not have.
type LeaseDenialTextSuite struct {
	suite.Suite
}

func TestLeaseDenialTextSuite(t *testing.T) { suite.Run(t, new(LeaseDenialTextSuite)) }

func (s *LeaseDenialTextSuite) TestListsAtMostFiveWritePaths() {
	row := types.Job{ID: "wide", WritePaths: []string{"a/**", "b/**", "c/**", "d/**", "e/**", "f/**", "g/**"}}
	s.Equal("a/**, b/**, c/**, d/**, e/** and 2 more (magus describe job wide)", writePathsShown(row))
}

func (s *LeaseDenialTextSuite) TestListsFiveWritePathsWhole() {
	row := types.Job{ID: "narrow", WritePaths: []string{"a/**", "b/**", "c/**", "d/**", "e/**"}}
	s.Equal("a/**, b/**, c/**, d/**, e/**", writePathsShown(row))
}

func (s *LeaseDenialTextSuite) TestTheRewriteRefusalTrimsItsWritePaths() {
	row := types.Job{ID: "wide", WritePaths: []string{"a/**", "b/**", "c/**", "d/**", "e/**", "f/**"}}
	got := rewriteDenial(runVerdict{run: targetRun{verb: "run", target: "generate", projects: []string{"."}},outside: []string{"z.go"}}, row, nil)
	s.Contains(got.Why, "and 1 more (magus describe job wide)")
	s.NotContains(got.Why, "f/**")
}

func (s *LeaseDenialTextSuite) TestTheCheckoutRefusalSaysTheCheckoutHasNoMagus() {
	held, other := markedWorkspace(s.T()), markedWorkspace(s.T())
	ctx, deps, row := heldRowFixture(s.T(), held, held, "feat")
	got := denyLeaseCheckout(ctx, deps, row.ID, fmt.Sprintf("cd %s && ./magus run go-test .", other))
	s.Require().True(got.refused())
	s.Contains(got.Say, "which has no ./magus yet")
	s.NotContains(got.Say, "&& ./magus")
}
