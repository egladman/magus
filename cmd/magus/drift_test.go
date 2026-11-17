package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/job"
)

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
