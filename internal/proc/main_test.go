package proc_test

import (
	"os"
	"testing"
)

// TestMain clears MAGUS_DAEMON_SOCKET before the suite runs so the package is
// hermetic. proc.New returns ErrAlreadyAdopted when that var is set; the tests that
// exercise adoption set it themselves via t.Setenv. But when the suite runs under
// `magus run` with a daemon active, magus injects MAGUS_DAEMON_SOCKET into the test
// subprocess (the recursive-call convention), tripping the guard before any test
// opts in — so `magus run test`/`coverage` failed every proc test even though plain
// `go test` passed. Clearing it here can't be done per-test: three sibling tests use
// t.Parallel, which forbids t.Setenv.
func TestMain(m *testing.M) {
	os.Unsetenv("MAGUS_DAEMON_SOCKET")
	os.Exit(m.Run())
}
