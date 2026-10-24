package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGateWaiveTakesReasonOrRevoke(t *testing.T) {
	for _, args := range [][]string{
		{"HEAD"},
		{"--revoke", "--reason", "x", "HEAD"},
	} {
		err := gateWaive(t.Context(), t.TempDir(), args)
		require.Error(t, err, "%q", args)
		assert.Equal(t, "magus gate waive: takes --reason to waive or --revoke to remove, and not both", err.Error(), "%q", args)
	}
	err := gateWaive(t.Context(), t.TempDir(), []string{"--reason", "x"})
	require.Error(t, err)
	assert.Equal(t, "magus gate waive: requires one commit", err.Error())
}

func TestGateRequiresASubcommand(t *testing.T) {
	err := gateCmd(t.Context(), t.TempDir(), nil)
	require.Error(t, err)
	assert.Equal(t, "magus gate: requires a subcommand (waive)", err.Error())
}
