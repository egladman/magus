package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// standingWorker is a worker row on a checkout its fork made.
type standingWorker struct {
	suite.Suite
	ctx    context.Context
	repo   worktreeRepo
	own    string
	worker types.Job
}

func (s *standingWorker) SetupTest() {
	s.repo = newWorktreeRepository(s.T())
	s.own = s.repo.add("lease-job")
	s.worker = narrowLease()
	s.worker.Parent = "orchestrator"
	s.worker.CheckoutRoot = s.own
	s.worker.Worktree = &types.JobWorktree{Root: s.own}
	ctx, _ := fleetFixture(s.T(), s.worker)
	s.ctx = job.WithSnapshot(ctx, job.Snapshot{Rows: []types.Job{s.worker}})
}

func (s *standingWorker) from(dir, line string) string {
	return denyLeaseScopedVCS(s.ctx, Dependencies{callDir: dir}, s.worker.ID, line)
}

type WorkerMoveSuite struct{ standingWorker }

func TestWorkerMoveSuite(t *testing.T) { suite.Run(t, new(WorkerMoveSuite)) }

func (s *WorkerMoveSuite) spelledOut() string {
	return fmt.Sprintf("`git -C %s mv <old> <new>`", s.own)
}

func (s *WorkerMoveSuite) TestALoneMovePasses() {
	s.Empty(s.from(s.own, "git mv a.txt b.txt"))
}

func (s *WorkerMoveSuite) TestAChainedMovePasses() {
	s.Empty(s.from(s.own, "git mv a.txt b.txt; git mv c.txt d.txt"))
}

func (s *WorkerMoveSuite) TestALoopOverLiteralWordsPasses() {
	s.Empty(s.from(s.own, "for f in a.txt b.txt; do git mv $f new/$f; done"))
}

func (s *WorkerMoveSuite) TestAQuotedLoopVariablePasses() {
	s.Empty(s.from(s.own, `for f in a.txt b.txt; do git mv "$f" "new/$f"; done`))
}

func (s *WorkerMoveSuite) TestALoopClimbingOutIsRefused() {
	reason := s.from(s.own, "for f in a.txt; do git mv $f ../$f; done")
	s.Contains(reason, "a worker moves only paths its line spells out")
	s.Contains(reason, s.spelledOut())
}

func (s *WorkerMoveSuite) TestAnUnboundVariableIsRefusedWithTheSpelledOutLine() {
	reason := s.from(s.own, `git mv "$src" "$dst"`)
	s.Contains(reason, "a worker moves only paths its line spells out")
	s.Contains(reason, s.spelledOut())
}

// The word renders as `.txt`, a path the shell would not pass.
func (s *WorkerMoveSuite) TestAVariableBesideTextIsRefused() {
	s.Contains(s.from(s.own, "git mv $src.txt new.txt"), "not one literal string")
}

func (s *WorkerMoveSuite) TestALoopOverACommandSubstitutionIsRefused() {
	s.Contains(s.from(s.own, "for f in $(ls); do git mv $f new/$f; done"), "where it moves cannot be read")
}

func (s *WorkerMoveSuite) TestAShellPayloadIsRefused() {
	s.Contains(s.from(s.own, `sh -c 'for f in a; do git mv $f.txt outside/$f.txt; done'`), "shell payload")
}

func (s *WorkerMoveSuite) TestAMoveInAConditionalIsRefusedAsAMove() {
	reason := s.from(s.own, "if true; then git mv a.txt b.txt; fi")
	s.Contains(reason, "a worker moves only paths its line spells out")
	s.NotContains(reason, "a worker commits only")
}

func (s *WorkerMoveSuite) TestAMoveFromAnotherCheckoutNamesItsOwn() {
	s.Contains(s.from(s.repo.main, "git mv a.txt b.txt"), fmt.Sprintf("`git -C %s mv a.txt b.txt`", s.own))
}

func (s *WorkerMoveSuite) TestALoopFromAnotherCheckoutIsRefused() {
	s.Contains(s.from(s.repo.main, "for f in a.txt; do git mv $f new/$f; done"), "outside "+s.own)
}

func (s *WorkerMoveSuite) TestAnAliasNamedMvStaysAnAlias() {
	s.Contains(s.from(s.own, "git -c alias.mv=push mv origin"), "`git -c alias.mv`")
}

type WorkerReattachSuite struct{ standingWorker }

func TestWorkerReattachSuite(t *testing.T) { suite.Run(t, new(WorkerReattachSuite)) }

func (s *WorkerReattachSuite) TestSwitchForceCreateOnItsOwnBranchPasses() {
	s.Empty(s.from(s.own, "git switch -C "+s.worker.ID))
}

func (s *WorkerReattachSuite) TestCheckoutBOnItsOwnBranchPasses() {
	s.Empty(s.from(s.own, "git checkout -B "+s.worker.ID))
}

func (s *WorkerReattachSuite) TestAStartPointOfHEADPasses() {
	s.Empty(s.from(s.own, fmt.Sprintf("git switch -C %s HEAD", s.worker.ID)))
}

func (s *WorkerReattachSuite) TestAnotherBranchNameIsRefused() {
	s.Contains(s.from(s.own, "git switch -C other"), "`git switch`")
}

func (s *WorkerReattachSuite) TestTheBaseBranchIsRefused() {
	s.Contains(s.from(s.own, "git checkout -B main"), "`git checkout`")
}

func (s *WorkerReattachSuite) TestAnotherStartPointIsRefused() {
	s.Contains(s.from(s.own, fmt.Sprintf("git switch -C %s origin/main", s.worker.ID)), "`git switch`")
}

func (s *WorkerReattachSuite) TestAForceIsRefused() {
	s.Contains(s.from(s.own, fmt.Sprintf("git checkout -f -B %s", s.worker.ID)), "`git checkout`")
}

func (s *WorkerReattachSuite) TestAMergeIsRefused() {
	s.Contains(s.from(s.own, fmt.Sprintf("git switch -C %s --merge", s.worker.ID)), "`git switch`")
}

func (s *WorkerReattachSuite) TestAVariableBesideTheNameIsRefused() {
	s.Contains(s.from(s.own, fmt.Sprintf(`git switch -C "$prefix"%s`, s.worker.ID)), "not one literal string")
}

func (s *WorkerReattachSuite) TestFromAnotherCheckoutItNamesItsOwn() {
	s.Contains(s.from(s.repo.main, "git switch -C "+s.worker.ID), fmt.Sprintf("`git -C %s switch -C %s`", s.own, s.worker.ID))
}

func (s *WorkerReattachSuite) TestARowWithNoCheckoutOfItsOwnIsRefused() {
	row := s.worker
	row.Worktree = nil
	ctx := job.WithSnapshot(s.ctx, job.Snapshot{Rows: []types.Job{row}})
	s.Contains(denyLeaseScopedVCS(ctx, Dependencies{callDir: s.own}, row.ID, "git switch -C "+row.ID), "`git switch`")
}

func (s *WorkerReattachSuite) TestADetachedCommitNamesTheLineThatKeepsIt() {
	s.repo.git(s.own, "switch", "--detach")
	reason := s.from(s.own, "git commit -m done")
	s.Contains(reason, "has no branch checked out")
	s.Contains(reason, fmt.Sprintf("`git switch -C %s`", s.worker.ID))
}

// LeaseWriteMoveSuite grades what a move writes against a row's write paths.
type LeaseWriteMoveSuite struct {
	suite.Suite
	ctx context.Context
}

func TestLeaseWriteMoveSuite(t *testing.T) { suite.Run(t, new(LeaseWriteMoveSuite)) }

func (s *LeaseWriteMoveSuite) hold(writes []string, denies ...string) {
	ctx, root := fleetFixture(s.T(), types.Job{ID: "mover", WritePaths: writes, DenyPaths: denies, State: types.StateRunning, Registered: 1})
	s.ctx = ctx
	for _, file := range []string{"libs/old/a.go", "libs/old/sub/b.go"} {
		s.Require().NoError(os.MkdirAll(filepath.Join(root, filepath.Dir(file)), 0o755))
		s.Require().NoError(os.WriteFile(filepath.Join(root, file), nil, 0o644))
	}
}

func (s *LeaseWriteMoveSuite) deny(line string) string {
	return denyWriteOutsideLease(s.ctx, Dependencies{}, "mover", line)
}

func (s *LeaseWriteMoveSuite) TestEveryFileOnTheRowUnderBothNamesPasses() {
	s.hold([]string{"libs/old/a.go", "libs/old/sub/b.go", "libs/new/a.go", "libs/new/sub/b.go"})
	s.Empty(s.deny("git mv libs/old libs/new"))
}

func (s *LeaseWriteMoveSuite) TestALandingOffTheRowIsNamed() {
	s.hold([]string{"libs/old/a.go", "libs/old/sub/b.go", "libs/new/a.go"})
	s.Contains(s.deny("git mv libs/old libs/new"), "libs/new holds libs/new/sub/b.go, which is outside")
}

func (s *LeaseWriteMoveSuite) TestALandingOnADenyPathIsNamed() {
	s.hold([]string{"libs/old/**", "libs/new/**"}, "libs/new/sub/**")
	s.Contains(s.deny("git mv libs/old libs/new"), "libs/new holds libs/new/sub/b.go, which is outside")
}

func (s *LeaseWriteMoveSuite) TestASourceFileOffTheRowIsNamed() {
	s.hold([]string{"libs/old/a.go", "libs/new/a.go", "libs/new/sub/b.go"})
	s.Contains(s.deny("git mv libs/old libs/new"), "libs/old holds libs/old/sub, which is outside")
}

func (s *LeaseWriteMoveSuite) TestAFileMovedToAnUndeclaredNameIsRefused() {
	s.hold([]string{"libs/old/a.go", "libs/new/a.go"})
	s.Contains(s.deny("git mv libs/old/a.go libs/new"), "libs/new is outside")
}

func (s *LeaseWriteMoveSuite) TestALoopToAnOutsidePathIsRefused() {
	s.hold([]string{"libs/old/a.go"})
	s.Contains(s.deny("for f in a.go; do git mv libs/old/$f libs/elsewhere/$f; done"), "libs/elsewhere/a.go is outside")
}

func (s *LeaseWriteMoveSuite) TestALoopWithTextBesideItsVariableIsGraded() {
	s.hold([]string{"libs/old/a.go"})
	s.Contains(s.deny("for f in a; do git mv libs/old/$f.go libs/elsewhere/$f.go; done"), "libs/elsewhere/a.go is outside")
}

func (s *LeaseWriteMoveSuite) TestAWrappedLoopToAnOutsidePathIsRefused() {
	s.hold([]string{"libs/old/a.go"})
	s.Contains(s.deny("for f in a.go; do env X=1 git mv libs/old/$f libs/elsewhere/$f; done"), "libs/elsewhere/a.go is outside")
}

func (s *LeaseWriteMoveSuite) TestALoopFromOutsideTheRowIsRefused() {
	s.hold([]string{"libs/new/a.go"})
	s.Contains(s.deny("for f in a.go; do git mv libs/old/$f libs/new/$f; done"), "libs/old/a.go is outside")
}

func (s *LeaseWriteMoveSuite) TestALoopedRemoveIsGraded() {
	s.hold([]string{"libs/new/a.go"})
	s.Contains(s.deny("for f in a; do git rm libs/old/$f.go; done"), "libs/old/a.go is outside")
}

func (s *LeaseWriteMoveSuite) TestALoopInsideTheRowPasses() {
	s.hold([]string{"libs/old/a.go", "libs/new/a.go"})
	s.Empty(s.deny("for f in a.go; do git mv libs/old/$f libs/new/$f; done"))
}

// A claim covers its file at the path level, so moving the file passes.
func (s *LeaseWriteMoveSuite) TestAClaimOnOneDeclarationCoversAMoveOfItsFile() {
	s.hold([]string{"libs/old/a.go#project.name", "libs/new/a.go"})
	s.Empty(s.deny("git mv libs/old/a.go libs/new/a.go"))
}
