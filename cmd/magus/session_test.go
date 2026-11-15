package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/require"
)

// The helpers below are shared by test files that other groups still own: session_brief_test,
// session_cost_test, agent_harness_test, graph_test, diff_test and verbosity_test. The tests
// that once sat beside them moved to the packages that own the behavior.

// emitJournalEvent pushes one event through h along the real capture path, rather than
// hand-building the slog.Record. The attribute key that carries an Event is private
// to internal/journal, so a hand-built record would be testing a copy of it.
func emitJournalEvent(t *testing.T, h slog.Handler, e journal.Event) {
	t.Helper()
	journal.Emit(journal.WithLogger(context.Background(), journal.NewLogger(h)), e)
}

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

// withInteractiveTTY makes isInteractiveTTY() report true for the duration of t.
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

// captureWarnings installs a slog handler for the duration of fn and returns what it
// logged.
func captureWarnings(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fn()
	return buf.String()
}

// journalRecordSink keeps the slog.Record journal.Emit builds, so a test can replay the
// real encoding into another handler and see what that handler RETURNS. Going through
// journal.NewLogger instead would drop the error: the fan-out swallows child errors by
// contract, which is the very thing under test here.
type journalRecordSink struct{ records []slog.Record }

func (s *journalRecordSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *journalRecordSink) Handle(_ context.Context, r slog.Record) error {
	s.records = append(s.records, r.Clone())
	return nil
}

func (s *journalRecordSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *journalRecordSink) WithGroup(string) slog.Handler      { return s }
