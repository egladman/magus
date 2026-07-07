package main

import (
	"os"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

// TestMain lets the test binary act as the `magus` command inside testscript
// scripts: `exec magus ...` in a .txtar file runs the real CLI in process (via
// run), so behavior tests exercise the actual command, not a mock.
func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"magus": runCLI,
	}))
}

// TestScripts replays every testdata/script/*.txtar as a black-box CLI behavior
// test: readable command-plus-expected-output scenarios that catch any observable
// change to the CLI. Each script runs in its own temp dir with the daemon off, so
// tests are hermetic and never touch a real workspace or socket.
func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(e *testscript.Env) error {
			e.Setenv("MAGUS_DAEMON_ENABLED", "false")
			e.Setenv("MAGUS_HINTS_ENABLED", "false")
			return nil
		},
	})
}
