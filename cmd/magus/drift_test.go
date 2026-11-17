package main

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// resolveGitDriver resolves dir's real git driver through vcs.Resolve; vcs_test.go reads it.
func resolveGitDriver(t *testing.T, dir string) types.VCSDriver { //nolint:ireturn // the VCS driver is picked by detection at runtime
	t.Helper()
	res, err := vcs.Resolve(context.Background(), dir, "", types.VCSOptions{})
	require.NoError(t, err)
	require.NotNil(t, res.VCS)
	require.Equal(t, "git", res.Name)
	return res.VCS
}

// TestParseCheckDriftArgsReadsWhatTheJobSubmits pins that the worker reads back exactly the hook
// the submitter wrote, and that a run with no flags, the one an older drift section starts, is a
// commit's.
func TestParseCheckDriftArgsReadsWhatTheJobSubmits(t *testing.T) {
	want := job.DriftHook{Hook: job.DriftHookPrePush, Remote: "origin", Pushes: []job.DriftPush{
		{Remote: strings.Repeat("b", 40), Local: strings.Repeat("a", 40)},
	}}
	got, err := parseCheckDriftArgs(want.Argv()[2:])
	require.NoError(t, err)
	assert.Equal(t, want, got)

	plain, err := parseCheckDriftArgs(nil)
	require.NoError(t, err)
	assert.Equal(t, job.DriftHook{Hook: job.DriftHookPostCommit}, plain)
}
