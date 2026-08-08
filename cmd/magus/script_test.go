package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

// TestMain lets the test binary act as the `magus` command inside testscript
// scripts: `exec magus ...` in a .txtar file runs the real CLI in process (via
// run), so behavior tests exercise the actual command, not a mock.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"magus": func() { os.Exit(runCLI()) },
	})
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
			// The shipped guard templates, by ABSOLUTE PATH to the real files.
			// A script that copied them into its own archive would be testing a
			// copy, and a copy of the artifact readers download is exactly what
			// dogfood_test.go exists to prevent.
			templates, err := filepath.Abs(filepath.Join("..", "..", "docs", "guides", "integrations", "agents"))
			if err != nil {
				return err
			}
			e.Setenv("GUARD_TEMPLATES", templates)
			return nil
		},
	})
}
