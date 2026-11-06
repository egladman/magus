package guard

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

// DenialRouteSuite pins two denials that left an agent with no next step: a
// lease list printed whole, and a dry run no command provides.
type DenialRouteSuite struct{ suite.Suite }

func TestDenialRouteSuite(t *testing.T) { suite.Run(t, new(DenialRouteSuite)) }

func (s *DenialRouteSuite) TestALongLeaseNamesItsNearestPathsAndCountsTheRest() {
	paths := []string{"docs/a.md", "docs/b.md", "internal/job/fork.go", "internal/job/fork_test.go", "internal/job/wait.go"}
	for i := range 40 {
		paths = append(paths, fmt.Sprintf("libs/x/f%d.go", i))
	}
	got := nearestWritePaths(types.Job{ID: "big", WritePaths: paths}, "internal/job/exit.go")
	s.Equal("internal/job/fork.go, internal/job/fork_test.go, internal/job/wait.go and 42 more; `magus describe job big` lists them", got)
}

func (s *DenialRouteSuite) TestAShortLeaseIsNamedWhole() {
	s.Equal("a.go, b.go", nearestWritePaths(types.Job{ID: "small", WritePaths: []string{"a.go", "b.go"}}, "c.go"))
}

func (s *DenialRouteSuite) TestAContractPickNamesTheForkThatLandsIt() {
	root, _, decls, _ := contractCheckout(s.T())
	got := denyContractPick(context.Background(), Dependencies{}, location{workspace: root, dir: root}, "git cherry-pick "+decls, DialectBash)
	s.Contains(got, "job fork <integration> --checkout <dir>")
	s.NotContains(got, "dry run")
}
