package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The helpers below are shared by test files that other groups still own:
// agent_harness_test, graph_test, diff_test and verbosity_test. The tests that once sat
// beside them moved to the packages that own the behavior.

func loadStream(t *testing.T, root string, lines ...string) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.ndjson")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))

	var err error
	out := captureStdout(t, func() { err = sessionLoad(t.Context(), root, []string{"--file", path}) })
	return out, err
}

// attentionTestRoot isolates one test's queue: the store is keyed by repository
// identity under the user state dir, so without both of these a test would file
// requests into whatever repo the test binary happens to be running in.
//
// Dispose refuses without an interactive terminal, and the test binary's own
// stdin/stderr are not one, so every test is given a fake terminal by default.
func attentionTestRoot(t *testing.T) string {
	t.Helper()
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	withInteractiveTTY(t)
	return t.TempDir()
}

// withInteractiveTTY makes interactive.IsInteractiveTTY() report true for the duration of t.
// FixedProbe answers every descriptor as a terminal, so it stands in for both stdin and
// stderr's checks at once.
func withInteractiveTTY(t *testing.T) {
	t.Helper()
	prev := termkit.SystemProbe
	termkit.SystemProbe = termkit.FixedProbe(80, 24)
	t.Cleanup(func() { termkit.SystemProbe = prev })
}

type neverTerminalProbe struct{}

func (neverTerminalProbe) IsTerminal(uintptr) bool        { return false }
func (neverTerminalProbe) Size(uintptr) (int, int, error) { return 0, 0, nil }

func TestSessionShowNamesTheCostSectionsItCouldNotMeasure(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := t.TempDir()
	_, err := loadStream(t, root, `{"host":"h1","session":"s1","ts":1,"kind":"shell.command","ref":"r1","text":"ls"}`)
	require.NoError(t, err)

	out := captureStdout(t, func() { require.NoError(t, sessionShow(root, []string{"s1"})) })

	assert.Contains(t, out, "Cost, with the root as the main thread:")
	assert.Contains(t, out, "root context     not measured: this checkout's trail holds nothing for the session")
	assert.NotContains(t, out, "$0.00")
}
