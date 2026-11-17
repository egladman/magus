package server

import (
	"errors"
	"fmt"
	"os"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/proc"
)

// BrokerSpawnTrapEnv names a file. While it is set, SpawnBroker starts nothing: it appends
// a line to the file and fails, so a test sees that a start was attempted and no detached
// process outlives it. The test binary is re-run as `broker` otherwise, which runs the whole
// suite again.
const BrokerSpawnTrapEnv = "MAGUS_BROKER_SPAWN_TRAP"

var errBrokerSpawnTrapped = errors.New("a unit test never starts a real broker")

// SpawnBroker starts a broker detached from this process, logging to BrokerLogPath.
func SpawnBroker() (pid int, logPath string, err error) {
	logPath = BrokerLogPath()
	if trap := os.Getenv(BrokerSpawnTrapEnv); trap != "" {
		return 0, logPath, trapBrokerSpawn(trap)
	}
	pid, err = proc.SpawnDetached([]string{"server", "broker", "--" + flaggen.FlagServerBrokerLog, logPath}, logPath)
	return pid, logPath, err
}

func trapBrokerSpawn(trap string) error {
	f, err := os.OpenFile(trap, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("%w: %w", errBrokerSpawnTrapped, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintln(f, "spawn"); err != nil {
		return fmt.Errorf("%w: %w", errBrokerSpawnTrapped, err)
	}
	return errBrokerSpawnTrapped
}
