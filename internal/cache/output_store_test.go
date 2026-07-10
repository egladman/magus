package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/journal"
	runPkg "github.com/egladman/magus/internal/proc/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOutputStorePersistLookupRoundTrip persists one execution's records and reads its
// reconstructed text and derived metadata back by ref.
func TestOutputStorePersistLookupRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := newOutputStore(dir)

	desc0 := OutputDescriptor{Project: "svc/api", Target: "test", Failed: true, ErrMsg: "boom", TimestampMs: 1_700_000_000_000, DurationMs: 1200}
	ref, err := s.persist("deadbeefcafef00d", []byte("lint: undefined symbol foo\n"), desc0)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(ref, RefPrefix))
	assert.Len(t, ref, len(RefPrefix)+refHexLen)

	data, desc, err := OutputByRef(dir, ref)
	require.NoError(t, err)
	assert.Equal(t, "lint: undefined symbol foo\n", string(data), "output is returned verbatim from the blob")

	assert.Equal(t, OutputDescriptor{
		Ref: ref, Project: "svc/api", Target: "test",
		Failed: true, ErrMsg: "boom", TimestampMs: 1_700_000_000_000, DurationMs: 1200,
	}, desc)

	// The structured form returns the DOMAIN records (output line + result), which the
	// handler layer maps onto the wire proto - no intermediate byte representation.
	recs, rdesc, err := OutputEventsByRef(dir, ref)
	require.NoError(t, err)
	require.Len(t, recs, 2, "one output record + one result record")
	assert.Equal(t, journal.KindOutput, recs[0].Kind)
	assert.Equal(t, "lint: undefined symbol foo", recs[0].Text)
	assert.Equal(t, journal.KindResult, recs[1].Kind)
	assert.Equal(t, ref, recs[1].Ref)
	assert.Equal(t, desc, rdesc, "OutputEventsByRef returns the same descriptor as OutputByRef")
}

// TestOutputStoreVerbatimFidelity pins the reason for the blob store: `magus query ref` returns
// the EXACT bytes the process wrote. The old reconstruct-from-line-records path re-added a
// trailing newline to output that had none (printf "done"); the verbatim blob does not.
func TestOutputStoreVerbatimFidelity(t *testing.T) {
	dir := t.TempDir()
	s := newOutputStore(dir)
	for _, raw := range []string{
		"done",             // no trailing newline
		"a\nb\nc\n",        // trailing newline preserved
		"with\ttabs\r\nCR", // control chars + CRLF, no final newline
		"",                 // empty output
	} {
		ref, err := s.persist("k", []byte(raw), OutputDescriptor{Project: "p", Target: "t"})
		require.NoError(t, err)
		got, _, err := OutputByRef(dir, ref)
		require.NoError(t, err)
		assert.Equal(t, raw, string(got), "output must round-trip byte-for-byte")
	}
}

// TestOutputStorePerExecutionRefsAreDistinct verifies repeated executions of ONE cache
// key each get their own addressable ref (keep-last-K history).
func TestOutputStorePerExecutionRefsAreDistinct(t *testing.T) {
	s := newOutputStore(t.TempDir())
	const key = "samekey00"

	ref1, err := s.persist(key, []byte("run 1\n"), OutputDescriptor{Project: "p", Target: "build"})
	require.NoError(t, err)
	ref2, err := s.persist(key, []byte("run 2\n"), OutputDescriptor{Project: "p", Target: "build"})
	require.NoError(t, err)

	assert.NotEqual(t, ref1, ref2, "two executions of one cache key must mint distinct refs")
	assert.Equal(t, ref2, s.latestRef(key), "latestRef returns the newest execution's ref")
}

// TestOutputStoreKeepLastK bounds retention to defaultOutputKeepLast newest executions
// per cache key; the newest survives.
func TestOutputStoreKeepLastK(t *testing.T) {
	dir := t.TempDir()
	s := newOutputStore(dir)
	const key = "boundedkey"

	var last string
	for i := 0; i < defaultOutputKeepLast+3; i++ {
		ref, err := s.persist(key, []byte("run\n"), OutputDescriptor{Project: "p", Target: "build"})
		require.NoError(t, err)
		last = ref
	}

	files, err := os.ReadDir(filepath.Join(dir, "outputs", key))
	require.NoError(t, err)
	outs := 0
	for _, f := range files {
		if strings.HasSuffix(f.Name(), outExt) {
			outs++
		}
	}
	assert.Equal(t, defaultOutputKeepLast, outs, "retention keeps exactly K executions (each a blob + descriptor)")
	_, _, err = OutputByRef(dir, last)
	assert.NoError(t, err, "the newest execution survives pruning")
}

// TestOutputStorePrefixAndAmbiguity covers git-style prefix resolution.
func TestOutputStorePrefixAndAmbiguity(t *testing.T) {
	dir := t.TempDir()
	s := newOutputStore(dir)

	ref, err := s.persist("k1", []byte("body\n"), OutputDescriptor{Project: "p", Target: "build"})
	require.NoError(t, err)
	data, _, err := OutputByRef(dir, ref)
	require.NoError(t, err)
	assert.Equal(t, "body\n", string(data))

	_, err = s.persist("k2", []byte("other\n"), OutputDescriptor{Project: "p", Target: "build"})
	require.NoError(t, err)
	_, _, err = OutputByRef(dir, RefPrefix) // the bare prefix matches both
	var amb *AmbiguousRefError
	require.True(t, errors.As(err, &amb), "a shared prefix should return *AmbiguousRefError, got %v", err)
	assert.Len(t, amb.Candidates, 2)

	_, _, err = OutputByRef(dir, "refffffffff")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

// TestInvocationByID reads a union run log and rebuilds the invocation header (command +
// outcome), covering InvocationByID and readEvents.
func TestInvocationByID(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, RunsDir)
	require.NoError(t, os.MkdirAll(runs, 0o755))
	f, err := os.Create(filepath.Join(runs, "inv123.jsonl"))
	require.NoError(t, err)
	enc := json.NewEncoder(f)
	require.NoError(t, enc.Encode(journal.Event{Kind: journal.KindStarted, Command: &journal.Command{Verb: "run", Args: []string{"build"}, Trigger: "agent"}}))
	require.NoError(t, enc.Encode(journal.Event{Kind: journal.KindFinished, Status: journal.StatusPass}))
	require.NoError(t, f.Close())

	inv, err := InvocationByID(dir, "inv123")
	require.NoError(t, err)
	assert.Equal(t, "inv123", inv.ID)
	assert.Equal(t, "run", inv.Command.Verb)
	assert.Equal(t, []string{"build"}, inv.Command.Args)

	_, err = InvocationByID(dir, "missing")
	assert.ErrorIs(t, err, fs.ErrNotExist, "an aged-out run log surfaces as fs.ErrNotExist")
}

// TestAmbiguousRefErrorMessage covers AmbiguousRefError.Error's rendering.
func TestAmbiguousRefErrorMessage(t *testing.T) {
	e := &AmbiguousRefError{Prefix: "refde", Candidates: []string{"refdead", "refdeed"}}
	msg := e.Error()
	assert.Contains(t, msg, "refde")
	assert.Contains(t, msg, "ambiguous")
	assert.Contains(t, msg, "refdead")
	assert.Contains(t, msg, "refdeed")
}

// TestRunOutputNoTrailingNewline drives a real Run whose output lacks a final newline, covering
// lineTap.flush and confirming the blob store returns those bytes verbatim (no newline added).
func TestRunOutputNoTrailingNewline(t *testing.T) {
	root, cdir, c := newMutableCache(t)
	writeMain(t, root, "package main")

	step := makeStep(root)
	r, err := c.Run(context.Background(), step, func(ctx context.Context) error {
		stdout, _ := runPkg.OutputWriters(ctx)
		fmt.Fprint(stdout, "no newline here") // no '\n' -> the trailing partial line goes through flush
		return nil
	})
	require.NoError(t, err)

	ref := c.outputs.latestRef(r.Hash)
	require.NotEmpty(t, ref)
	data, _, err := OutputByRef(cdir, ref)
	require.NoError(t, err)
	assert.Equal(t, "no newline here", string(data), "verbatim - no newline invented")
}

// TestLooksLikeRef pins the query router's discriminator.
func TestLooksLikeRef(t *testing.T) {
	for _, s := range []string{"ref1a2b3c", "refdeadbeef", "refa", "ref0"} {
		assert.True(t, LooksLikeRef(s), "%q should be recognized as a ref", s)
	}
	for _, s := range []string{"refactor", "reference", "ref", "ref ", "kind:spell", "REF1A2B", "1a2b3c", ""} {
		assert.False(t, LooksLikeRef(s), "%q must NOT be treated as a ref", s)
	}
}

// TestRunPersistsOutputRef drives the real Run path and confirms captured output is
// persisted as records - and reconstructed by ref - for a passing miss and a failure.
func TestRunPersistsOutputRef(t *testing.T) {
	root, cdir, c := newMutableCache(t)
	writeMain(t, root, "package main")

	pass := makeStep(root)
	rPass, err := c.Run(context.Background(), pass, func(ctx context.Context) error {
		stdout, _ := runPkg.OutputWriters(ctx)
		fmt.Fprintln(stdout, "build ok: 3 files")
		return nil
	})
	require.NoError(t, err)
	require.False(t, rPass.Hit)

	passRef := c.outputs.latestRef(rPass.Hash)
	require.NotEmpty(t, passRef, "a passing miss should persist a ref")
	data, meta, err := OutputByRef(cdir, passRef)
	require.NoError(t, err)
	assert.Contains(t, string(data), "build ok: 3 files")
	assert.False(t, meta.Failed)
	assert.Equal(t, "test/pkg", meta.Project)

	fail := makeStep(root)
	fail.Target = "test"
	boom := errors.New("exit status 1")
	_, err = c.Run(context.Background(), fail, func(ctx context.Context) error {
		stdout, _ := runPkg.OutputWriters(ctx)
		fmt.Fprintln(stdout, "FAIL: assertion failed")
		return boom
	})
	require.ErrorIs(t, err, boom)

	failHash, herr := c.hashStep(context.Background(), &fail)
	require.NoError(t, herr)
	failRef := c.outputs.latestRef(failHash)
	require.NotEmpty(t, failRef, "a failing run should persist a ref")
	fdata, fmeta, err := OutputByRef(cdir, failRef)
	require.NoError(t, err)
	assert.Contains(t, string(fdata), "FAIL: assertion failed")
	assert.True(t, fmeta.Failed)
	assert.Equal(t, "exit status 1", fmeta.ErrMsg)
}

// TestOutputStoreRemoveForProject wipes one project's executions while leaving others.
func TestOutputStoreRemoveForProject(t *testing.T) {
	dir := t.TempDir()
	s := newOutputStore(dir)

	keep, err := s.persist("ka", []byte("a\n"), OutputDescriptor{Project: "keep/me", Target: "build"})
	require.NoError(t, err)
	gone, err := s.persist("kb", []byte("b\n"), OutputDescriptor{Project: "drop/me", Target: "build"})
	require.NoError(t, err)

	s.removeForProject("drop/me")

	_, _, err = OutputByRef(dir, gone)
	assert.ErrorIs(t, err, fs.ErrNotExist, "dropped project's execution should be gone")
	_, _, err = OutputByRef(dir, keep)
	assert.NoError(t, err, "other project's execution should remain")

	_, statErr := os.Stat(filepath.Join(dir, "outputs", "kb"))
	assert.ErrorIs(t, statErr, fs.ErrNotExist)
}
