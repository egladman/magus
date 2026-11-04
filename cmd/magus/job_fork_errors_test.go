package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
)

type forkErrorsSuite struct{ suite.Suite }

func TestForkErrors(t *testing.T) { suite.Run(t, new(forkErrorsSuite)) }

func (s *forkErrorsSuite) TestUsageFormattingPreservesTheCauseAndStatus() {
	cause := errors.New("cannot read the plan")
	format := "magus job fork: %w"
	err := exit.Usagef(format, cause)

	s.ErrorIs(err, cause)
	s.EqualError(err, "magus job fork: cannot read the plan")
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *forkErrorsSuite) TestUsageFormattingPreservesTypedCauses() {
	cause := &os.PathError{Op: "read", Path: "jobs.jsonl", Err: fs.ErrPermission}
	format := "magus job fork: %w"
	err := exit.Usagef(format, cause)

	var got *os.PathError
	s.Require().ErrorAs(err, &got)
	s.Same(cause, got)
	s.ErrorIs(err, fs.ErrPermission)
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *forkErrorsSuite) TestStackedHeadPreservesResolutionFailures() {
	repo, _ := execRepositoryFixture(s.T())
	globalCfg.VCS.Name = "not-a-vcs"
	row := types.Declaration{ID: "child", Delivery: types.DeliveryStacked}

	got, err := stackedHead(s.T().Context(), repo.Root(), row)

	s.ErrorIs(err, types.ErrVCSUnknown)
	s.ErrorContains(err, "child is stacked, and")
	s.ErrorContains(err, "name parent_head in the record")
	s.Equal(row, got)
}

func (s *forkErrorsSuite) TestStackedHeadPreservesMetadataFailures() {
	s.T().Cleanup(snapshotGlobals())
	globalCfg.VCS.Name = "git"
	row := types.Declaration{ID: "child", Delivery: types.DeliveryStacked}

	got, err := stackedHead(s.T().Context(), s.T().TempDir(), row)

	var cause *exec.ExitError
	s.ErrorAs(err, &cause)
	s.ErrorContains(err, "child is stacked, and")
	s.Equal(row, got)
}

func (s *forkErrorsSuite) TestForkPreservesTheStackedHeadCancellation() {
	resetWorkspaceMemo(s.T())
	repo, _ := execRepositoryFixture(s.T())
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()

	err := jobFork(ctx, repo.Root(), []string{"child", "--read-only", "--criteria", "inspect", "--delivery", "stacked"})

	s.ErrorIs(err, context.Canceled)
	s.ErrorContains(err, "magus job fork: child is stacked, and")
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *forkErrorsSuite) TestForkPreservesThePlanWriteFailure() {
	resetWorkspaceMemo(s.T())
	repo, _ := execRepositoryFixture(s.T())
	store, err := openJobs(s.T().Context(), repo.Root(), jobOpen{})
	s.Require().NoError(err)
	path, err := store.Path()
	s.Require().NoError(err)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o700))
	// Readers can inspect the absent plan; its writer cannot open the dangling lock link.
	s.Require().NoError(os.Symlink(filepath.Join(s.T().TempDir(), "missing", "lock"), path+".lock"))

	err = jobFork(s.T().Context(), repo.Root(), []string{"child", "--read-only", "--criteria", "inspect"})

	var cause *os.PathError
	s.Require().ErrorAs(err, &cause)
	s.Equal(path+".lock", cause.Path)
	s.ErrorIs(err, fs.ErrNotExist)
	s.ErrorContains(err, "magus job fork:")
	s.Equal(exit.Usage, exit.Code(err))
	rows, err := store.List()
	s.Require().NoError(err)
	s.Empty(rows)
}
