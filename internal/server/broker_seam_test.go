package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/testkit"
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

// A TestMain sets the trap once; each test's Isolate rebuilds the environment and must
// keep it.
func TestSpawnBrokerTrapSurvivesIsolate(t *testing.T) {
	trap := filepath.Join(t.TempDir(), "spawns")
	t.Setenv(BrokerSpawnTrapEnv, trap)
	testkit.Isolate(t)

	assert.Equal(t, trap, os.Getenv(BrokerSpawnTrapEnv))
	_, _, err := SpawnBroker()
	require.ErrorIs(t, err, errBrokerSpawnTrapped)
}

func TestSpawnBrokerTrapReportsAFileItCannotWrite(t *testing.T) {
	t.Setenv(BrokerSpawnTrapEnv, filepath.Join(t.TempDir(), "missing", "spawns"))

	_, _, err := SpawnBroker()
	require.ErrorIs(t, err, errBrokerSpawnTrapped)
}
