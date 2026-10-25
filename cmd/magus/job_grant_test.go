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
