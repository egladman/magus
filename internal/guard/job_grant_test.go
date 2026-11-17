package guard

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGrantableRuleNamesTheGrantableOnesWhenRefusing(t *testing.T) {
	require.NoError(t, GrantableRule("hooks-off"))
	err := GrantableRule("stage-all")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"stage-all" is not grantable`)
	assert.Contains(t, err.Error(), "hooks-off")
}

func TestGrantForCommitTakesReasonOrRevoke(t *testing.T) {
	for _, opts := range []GrantForCommitOptions{{}, {Revoke: true, Reason: "x"}} {
		err := GrantForCommit(t.Context(), &bytes.Buffer{}, t.TempDir(), "HEAD", []string{PushUnverifiedRule}, opts, nil)
		require.Error(t, err, "%+v", opts)
		assert.Equal(t, "magus job grant: --commit takes --reason to waive or --revoke to remove, and not both", err.Error(), "%+v", opts)
	}
}

func TestGrantForCommitScopesOnlyPushUnverified(t *testing.T) {
	opts := GrantForCommitOptions{Reason: "x"}
	err := GrantForCommit(t.Context(), &bytes.Buffer{}, t.TempDir(), "HEAD", nil, opts, nil)
	require.Error(t, err)
	assert.Equal(t, "magus job grant: --commit takes one rule, push-unverified", err.Error())

	err = GrantForCommit(t.Context(), &bytes.Buffer{}, t.TempDir(), "HEAD", []string{"hooks-off"}, opts, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hooks-off is granted to a job, not a commit")
}
