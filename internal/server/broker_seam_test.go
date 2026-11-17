package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpawnBrokerTrapCountsTheStartsItRefused(t *testing.T) {
	trap := filepath.Join(t.TempDir(), "spawns")
	t.Setenv(BrokerSpawnTrapEnv, trap)

	for range 2 {
		pid, _, err := SpawnBroker()
		require.ErrorIs(t, err, errBrokerSpawnTrapped)
		assert.Zero(t, pid)
	}

	got, err := os.ReadFile(trap)
	require.NoError(t, err)
	assert.Equal(t, "spawn\nspawn\n", string(got))
}

func TestSpawnBrokerTrapReportsAFileItCannotWrite(t *testing.T) {
	t.Setenv(BrokerSpawnTrapEnv, filepath.Join(t.TempDir(), "missing", "spawns"))

	_, _, err := SpawnBroker()
	require.ErrorIs(t, err, errBrokerSpawnTrapped)
}
