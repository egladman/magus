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
	s := NewOutputStore(dir)

	desc0 := OutputDescriptor{Project: "svc/api", Target: "test", Failed: true, ErrMsg: "boom", TimestampMs: 1_700_000_000_000, DurationMs: 1200}
	ref, err := s.Persist("deadbeefcafef00d", []byte("lint: undefined symbol foo\n"), desc0)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(ref, RefPrefix))
	assert.Len(t, ref, len(RefPrefix)+refHexLen)

	data, desc, err := s.ByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, "lint: undefined symbol foo\n", string(data), "output is returned verbatim from the blob")

	assert.Equal(t, OutputDescriptor{
		Ref: ref, Project: "svc/api", Target: "test",
		Failed: true, ErrMsg: "boom", TimestampMs: 1_700_000_000_000, DurationMs: 1200,
	}, desc)
}

// TestOutputStoreVerbatimFidelity pins the reason for the blob store: `magus query ref` returns
// the EXACT bytes the process wrote. The old reconstruct-from-line-records path re-added a
// trailing newline to output that had none (printf "done"); the verbatim blob does not.
func TestOutputStoreVerbatimFidelity(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)
	for _, raw := range []string{
		"done",             // no trailing newline
		"a\nb\nc\n",        // trailing newline preserved
		"with\ttabs\r\nCR", // control chars + CRLF, no final newline
		"",                 // empty output
	} {
		ref, err := s.Persist("k", []byte(raw), OutputDescriptor{Project: "p", Target: "t"})
		require.NoError(t, err)
		got, _, err := s.ByRef(ref)
		require.NoError(t, err)
		assert.Equal(t, raw, string(got), "output must round-trip byte-for-byte")
	}
}

// TestOutputStorePerExecutionRefsAreDistinct verifies repeated executions of ONE cache
// key each get their own addressable ref (keep-last-K history).
func TestOutputStorePerExecutionRefsAreDistinct(t *testing.T) {
	s := NewOutputStore(t.TempDir())
	const key = "samekey00"

	ref1, err := s.Persist(key, []byte("run 1\n"), OutputDescriptor{Project: "p", Target: "build"})
	require.NoError(t, err)
	ref2, err := s.Persist(key, []byte("run 2\n"), OutputDescriptor{Project: "p", Target: "build"})
	require.NoError(t, err)

	assert.NotEqual(t, ref1, ref2, "two executions of one cache key must mint distinct refs")
	assert.Equal(t, ref2, s.LatestRef(key), "latestRef returns the newest execution's ref")
}

// TestOutputStoreKeepLastK bounds retention to defaultOutputKeepLast newest executions
// per cache key; the newest survives.
func TestOutputStoreKeepLastK(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)
	const key = "boundedkey"

	var last string
	for i := 0; i < defaultOutputKeepLast+3; i++ {
		ref, err := s.Persist(key, []byte("run\n"), OutputDescriptor{Project: "p", Target: "build"})
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
	_, _, err = s.ByRef(last)
	assert.NoError(t, err, "the newest execution survives pruning")
}

// TestOutputStorePrefixAndAmbiguity covers git-style prefix resolution.
func TestOutputStorePrefixAndAmbiguity(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)

	ref, err := s.Persist("k1", []byte("body\n"), OutputDescriptor{Project: "p", Target: "build"})
	require.NoError(t, err)
	data, _, err := s.ByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, "body\n", string(data))

	_, err = s.Persist("k2", []byte("other\n"), OutputDescriptor{Project: "p", Target: "build"})
	require.NoError(t, err)
	_, _, err = s.ByRef(RefPrefix) // the bare prefix matches both
	var amb *AmbiguousRefError
	require.True(t, errors.As(err, &amb), "a shared prefix should return *AmbiguousRefError, got %v", err)
	assert.Len(t, amb.Candidates, 2)

	_, _, err = s.ByRef("refffffffff")
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

	inv, err := NewOutputStore(dir).InvocationByID("inv123")
	require.NoError(t, err)
	assert.Equal(t, "inv123", inv.ID)
	assert.Equal(t, "run", inv.Command.Verb)
	assert.Equal(t, []string{"build"}, inv.Command.Args)

	_, err = NewOutputStore(dir).InvocationByID("missing")
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
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")

	step := makeStep(root)
	r, err := c.Run(context.Background(), step, func(ctx context.Context) error {
		stdout, _ := runPkg.OutputWriters(ctx)
		fmt.Fprint(stdout, "no newline here") // no '\n' -> the trailing partial line goes through flush
		return nil
	})
	require.NoError(t, err)

	ref := c.outputs.LatestRef(r.Hash)
	require.NotEmpty(t, ref)
	data, _, err := c.outputs.ByRef(ref)
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

// TestIsMintedRef pins the exact-length ref shape used to scan free text: only ref + exactly
// refHexLen hex is accepted, so prefixes and coincidentally-hex words are rejected.
func TestIsMintedRef(t *testing.T) {
	for _, s := range []string{"ref1a2b3c4d", "refdeadbeef"} {
		assert.True(t, IsMintedRef(s), "%q should be a minted ref", s)
	}
	for _, s := range []string{"reface", "refed", "ref1a2b3c", "refa", "ref", "refactor", "ref1a2b3c4d5", ""} {
		assert.False(t, IsMintedRef(s), "%q must NOT be a minted ref", s)
	}
}

// TestRunPersistsOutputRef drives the real Run path and confirms captured output is
// persisted as records - and reconstructed by ref - for a passing miss and a failure.
func TestRunPersistsOutputRef(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")

	pass := makeStep(root)
	rPass, err := c.Run(context.Background(), pass, func(ctx context.Context) error {
		stdout, _ := runPkg.OutputWriters(ctx)
		fmt.Fprintln(stdout, "build ok: 3 files")
		return nil
	})
	require.NoError(t, err)
	require.False(t, rPass.Hit)

	passRef := c.outputs.LatestRef(rPass.Hash)
	require.NotEmpty(t, passRef, "a passing miss should persist a ref")
	data, meta, err := c.outputs.ByRef(passRef)
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
	failRef := c.outputs.LatestRef(failHash)
	require.NotEmpty(t, failRef, "a failing run should persist a ref")
	fdata, fmeta, err := c.outputs.ByRef(failRef)
	require.NoError(t, err)
	assert.Contains(t, string(fdata), "FAIL: assertion failed")
	assert.True(t, fmeta.Failed)
	assert.Equal(t, "exit status 1", fmeta.ErrMsg)
}

// TestOutputStoreRemoveForProject wipes one project's executions while leaving others.
func TestOutputStoreRemoveForProject(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)

	keep, err := s.Persist("ka", []byte("a\n"), OutputDescriptor{Project: "keep/me", Target: "build"})
	require.NoError(t, err)
	gone, err := s.Persist("kb", []byte("b\n"), OutputDescriptor{Project: "drop/me", Target: "build"})
	require.NoError(t, err)

	s.removeForProject("drop/me")

	_, _, err = s.ByRef(gone)
	assert.ErrorIs(t, err, fs.ErrNotExist, "dropped project's execution should be gone")
	_, _, err = s.ByRef(keep)
	assert.NoError(t, err, "other project's execution should remain")

	_, statErr := os.Stat(filepath.Join(dir, "outputs", "kb"))
	assert.ErrorIs(t, statErr, fs.ErrNotExist)
}
