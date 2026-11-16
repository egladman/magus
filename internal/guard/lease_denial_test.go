package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/stretchr/testify/suite"
)

// LeaseDenialTextSuite pins rows 898 and 567: a refusal stays short on a
// wide row, and never serves a ./magus the checkout does not have.
type LeaseDenialTextSuite struct {
	suite.Suite
}

func TestLeaseDenialTextSuite(t *testing.T) { suite.Run(t, new(LeaseDenialTextSuite)) }

func (s *LeaseDenialTextSuite) TestListsAtMostFiveWritePaths() {
	row := job.Job{ID: "wide", Paths: job.Paths{Write: []string{"a/**", "b/**", "c/**", "d/**", "e/**", "f/**", "g/**"}}}
	s.Equal("a/**, b/**, c/**, d/**, e/** and 2 more (magus describe job wide)", writePathsShown(row))
}

func (s *LeaseDenialTextSuite) TestListsFiveWritePathsWhole() {
	row := job.Job{ID: "narrow", Paths: job.Paths{Write: []string{"a/**", "b/**", "c/**", "d/**", "e/**"}}}
	s.Equal("a/**, b/**, c/**, d/**, e/**", writePathsShown(row))
}

func (s *LeaseDenialTextSuite) TestTheRewriteRefusalTrimsItsWritePaths() {
	row := job.Job{ID: "wide", Paths: job.Paths{Write: []string{"a/**", "b/**", "c/**", "d/**", "e/**", "f/**"}}}
	got := rewriteDenial(runVerdict{run: targetRun{verb: "run", target: "generate", projects: []string{"."}}, outside: []string{"z.go"}}, row, nil)
	s.Contains(got.Why, "and 1 more (magus describe job wide)")
	s.NotContains(got.Why, "f/**")
}

func (s *LeaseDenialTextSuite) TestTheRewriteRefusalNamesTheFilesInItsVerdict() {
	row := job.Job{ID: "docs", Paths: job.Paths{Write: []string{"docs/reference/*.md"}}}
	got := rewriteDenial(runVerdict{run: targetRun{verb: "run", target: "catalog-docs-generate", projects: []string{"docs"}}, outside: []string{"docs/concepts/spells/*.md", "docs/reference/buzz/*.md"}}, row, nil)
	s.Contains(got.Say, "`docs/concepts/spells/*.md`, `docs/reference/buzz/*.md`")
}

func (s *LeaseDenialTextSuite) TestTheRewriteRefusalCountsTheFilesItLeavesToItsWhy() {
	outside := []string{"a.md", "b.md", "c.md", "d.md", "e.md", "f.md", "g.md"}
	got := rewriteDenial(runVerdict{run: targetRun{verb: "run", target: "generate", projects: []string{"."}}, outside: outside}, job.Job{ID: "narrow"}, nil)
	s.Contains(got.Say, "`e.md` and 2 more outside")
	s.NotContains(got.Say, "f.md")
	s.Contains(got.Why, "`f.md`, `g.md`")
}

// hack/policy/binaries.buzz sends a leased worker to `<held>/magus --root
// <held>`, so this rule passes that line wherever a cd left the shell.
func (s *LeaseDenialTextSuite) TestTheCheckoutRulePassesTheBinaryRulesRemedy() {
	held, other := markedWorkspace(s.T()), markedWorkspace(s.T())
	ctx, deps, row := heldRowFixture(s.T(), held, held, "feat")
	s.Require().NoError(os.WriteFile(filepath.Join(held, "magus"), nil, 0o755))
	for _, command := range []string{
		fmt.Sprintf("%s/magus --root %s run go-test .", held, held),
		fmt.Sprintf("cd %s && %s/magus --root %s run go-test .", other, held, held),
		fmt.Sprintf("cd %s && %s/magus --root %s run go-test .", held, held, held),
	} {
		s.Empty(denyLeaseCheckout(ctx, deps, row.ID, command), command)
	}
}

func (s *LeaseDenialTextSuite) TestTheCheckoutRefusalSaysTheCheckoutHasNoMagus() {
	held, other := markedWorkspace(s.T()), markedWorkspace(s.T())
	ctx, deps, row := heldRowFixture(s.T(), held, held, "feat")
	got := denyLeaseCheckout(ctx, deps, row.ID, fmt.Sprintf("cd %s && ./magus run go-test .", other))
	s.Require().True(got.refused())
	s.Contains(got.Say, "which has no ./magus yet")
	s.NotContains(got.Say, "&& ./magus")
}
