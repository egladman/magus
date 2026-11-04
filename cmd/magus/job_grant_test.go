package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJobGrantRefusesAnUngrantableRuleNamingTheGrantableOnes(t *testing.T) {
	err := jobGrant(t.Context(), t.TempDir(), []string{"some/job", "stage-all"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"stage-all" is not grantable`)
	assert.Contains(t, err.Error(), "hooks-off")

	err = jobGrant(t.Context(), t.TempDir(), []string{"--revoke", "--for", "1h", "some/job", "hooks-off"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none with --revoke")
}

func TestJobGrantCommitTakesReasonOrRevoke(t *testing.T) {
	for _, args := range [][]string{
		{"--commit", "HEAD", "push-ungated"},
		{"--commit", "HEAD", "--revoke", "--reason", "x", "push-ungated"},
	} {
		err := jobGrant(t.Context(), t.TempDir(), args)
		require.Error(t, err, "%q", args)
		assert.Equal(t, "magus job grant: --commit takes --reason to waive or --revoke to remove, and not both", err.Error(), "%q", args)
	}
}

func TestJobGrantCommitScopesOnlyPushUngated(t *testing.T) {
	err := jobGrant(t.Context(), t.TempDir(), []string{"--commit", "HEAD", "--reason", "x"})
	require.Error(t, err)
	assert.Equal(t, "magus job grant: --commit takes one rule, push-ungated", err.Error())

	err = jobGrant(t.Context(), t.TempDir(), []string{"--commit", "HEAD", "--reason", "x", "hooks-off"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hooks-off is granted to a job, not a commit")

	err = jobGrant(t.Context(), t.TempDir(), []string{"--commit", "HEAD", "--reason", "x", "stage-all"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--commit takes push-ungated, not "stage-all"`)

	err = jobGrant(t.Context(), t.TempDir(), []string{"--commit", "HEAD", "--for", "1h", "--reason", "x", "push-ungated"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--for does not apply to --commit")
}

func TestJobGrantPushUngatedNeedsACommit(t *testing.T) {
	err := jobGrant(t.Context(), t.TempDir(), []string{"some/job", "push-ungated"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "waives the push gate for one commit, not a job")
	assert.Contains(t, err.Error(), "magus job grant --commit <commit> push-ungated --reason <text>")

	err = jobGrant(t.Context(), t.TempDir(), []string{"--reason", "x", "some/job", "hooks-off"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--reason records a commit waiver")
}
