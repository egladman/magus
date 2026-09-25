// Package testenv keeps a test binary off the machine it runs on. The per-user runtime
// directory holds the broker's socket and the server's, and a test that resolves it
// dials, claims capacity from, or binds beside whatever the person running the tests has
// up. Every test package whose binary links the socket directory calls [Main] or [Wrap]
// from its TestMain; a conventions test in the root package enforces that.
package testenv

import (
	"fmt"
	"os"
	"testing"

	"github.com/egladman/magus/internal/config"
)

// isolatedEnv is what a test process must not inherit from the person running it.
// MAGUS_BROKER is pinned off: a test that wants a broker starts one under the private
// runtime directory and passes it with magus.WithBroker and magus.WithBrokerPolicy.
var isolatedEnv = map[string]string{
	"MAGUS_BROKER":         "off",
	"MAGUS_PROC_SOCKET":    "",
	"MAGUS_SERVER_ADDRESS": "",
}

// Isolate points this process at a fresh private runtime directory, clears every
// MAGUS_* configuration variable, pins the broker off, and drops the sockets a parent
// magus exported. It returns the directory's removal, which the caller runs once the
// tests have finished.
//
// A suite run by magus inherits what the invoking job exported, and the merge queue's
// gate exports an absolute MAGUS_CACHE_DIR: every fixture workspace then shares one
// cache, so a test reads another's trail or is served another's cache entry. A test
// that wants a variable sets it with t.Setenv.
//
// The directory is made under os.TempDir with a short name: a unix socket path is
// capped near 104 bytes on macOS, and a t.TempDir path can already exceed it.
func Isolate() (cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "mgrt")
	if err != nil {
		return nil, fmt.Errorf("testenv: private runtime dir: %w", err)
	}
	if err := os.Setenv("XDG_RUNTIME_DIR", dir); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("testenv: %w", err)
	}
	for _, v := range config.EnvVarDocs() {
		if err := os.Unsetenv(v.EnvVar); err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("testenv: %w", err)
		}
	}
	for k, v := range isolatedEnv {
		if v == "" {
			err = os.Unsetenv(k)
		} else {
			err = os.Setenv(k, v)
		}
		if err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("testenv: %w", err)
		}
	}
	return func() { _ = os.RemoveAll(dir) }, nil
}

// M runs a test binary isolated. It satisfies testscript.TestingM, so a package whose
// TestMain belongs to testscript isolates through it too.
type M struct{ m *testing.M }

// Wrap returns m isolated: Run calls [Isolate], runs the tests, and cleans up.
func Wrap(m *testing.M) M { return M{m: m} }

// Run isolates the process, runs the tests and removes the private directory. It
// returns 1 without running anything when isolation fails, since a test that ran
// against the real runtime directory is the failure this package exists to prevent.
func (i M) Run() int {
	cleanup, err := Isolate()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer cleanup()
	return i.m.Run()
}

// Main is the whole TestMain of a package that needs nothing else.
func Main(m *testing.M) { os.Exit(Wrap(m).Run()) }
