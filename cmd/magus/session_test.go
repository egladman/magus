package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp/replay"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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

	out := captureStdout(t, func() { require.NoError(t, sessionShow(t.Context(), root, []string{"s1"})) })

	assert.Contains(t, out, "Cost, with the root as the main thread:")
	assert.Contains(t, out, "root context     not measured: this checkout's activity log holds nothing for the session")
	assert.NotContains(t, out, "$0.00")
}

type BriefStoppedRunsSuite struct {
	suite.Suite
	dir string
}

func TestBriefStoppedRunsSuite(t *testing.T) { suite.Run(t, new(BriefStoppedRunsSuite)) }

func (s *BriefStoppedRunsSuite) SetupTest() { s.dir = filepath.Join(s.T().TempDir(), "replay") }

func (s *BriefStoppedRunsSuite) record(ref string, finish bool) {
	log, err := replay.OpenLog(s.dir, ref, replay.Header{Script: "hack/dev/land.buzz", Args: []string{"--dry-run"}})
	s.Require().NoError(err)
	if finish {
		s.Require().NoError(log.Finish())
	}
	s.Require().NoError(log.Close())
}

func (s *BriefStoppedRunsSuite) brief() string {
	var b strings.Builder
	writeStoppedRuns(&b, s.dir)
	return b.String()
}

func (s *BriefStoppedRunsSuite) TestAStoppedRunIsListedWithItsResumeAndRestart() {
	s.record("out1", false)
	s.Equal("\ninterrupted recorded runs (magus resumes none by itself):\n"+
		"  out1 hack/dev/land.buzz stopped between calls\n"+
		"    resume:  magus buzz --record hack/dev/land.buzz -- --dry-run\n"+
		"    restart: magus buzz --record --restart hack/dev/land.buzz -- --dry-run\n", s.brief())
}

func (s *BriefStoppedRunsSuite) TestNoStoppedRunPrintsNothing() {
	s.Empty(s.brief(), "no replay directory at all")
	s.record("out1", true)
	s.Empty(s.brief(), "a finished run is not interrupted")
}
