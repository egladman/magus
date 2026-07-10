package cache

import (
	"context"
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

// out builds one stdout output record.
func out(text string) journal.Event {
	return journal.Event{Kind: journal.KindOutput, Stream: journal.StreamStdout, Text: text}
}

// result builds a result record for a project/target.
func result(project, target string, failed bool, ts int64) journal.Event {
	r := journal.Event{Kind: journal.KindResult, Project: project, Target: target, Ts: ts, Status: journal.StatusPass}
	if failed {
		r.Status = journal.StatusFail
	}
	return r
}

// TestOutputStorePersistLookupRoundTrip persists one execution's records and reads its
// reconstructed text and derived metadata back by ref.
func TestOutputStorePersistLookupRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := newOutputStore(dir)

	res := result("svc/api", "test", true, 100_000)
	res.Text = "boom"
	res.DurMs = 1200
	ref, err := s.persist("deadbeefcafef00d", []journal.Event{out("lint: undefined symbol foo")}, res)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(ref, RefPrefix))
	assert.Len(t, ref, len(RefPrefix)+refHexLen)

	data, meta, err := LookupOutput(dir, ref)
	require.NoError(t, err)
	assert.Equal(t, "lint: undefined symbol foo\n", string(data), "text is reconstructed from output records")

	assert.Equal(t, OutputMeta{
		Ref: ref, CacheKey: "deadbeefcafef00d", Project: "svc/api", Target: "test",
		Failed: true, Err: "boom", Timestamp: 100, DurationMs: 1200,
	}, meta)

	// The structured form returns the DOMAIN records (output line + result), which the
	// handler layer maps onto the wire proto - no intermediate byte representation.
	recs, rmeta, err := LookupEvents(dir, ref)
	require.NoError(t, err)
	require.Len(t, recs, 2, "one output record + one result record")
	assert.Equal(t, journal.KindOutput, recs[0].Kind)
	assert.Equal(t, "lint: undefined symbol foo", recs[0].Text)
	assert.Equal(t, journal.KindResult, recs[1].Kind)
	assert.Equal(t, ref, recs[1].Ref)
	assert.Equal(t, meta, rmeta, "LookupEvents derives the same meta as LookupOutput")
}

// TestOutputStorePerExecutionRefsAreDistinct verifies repeated executions of ONE cache
// key each get their own addressable ref (keep-last-K history).
func TestOutputStorePerExecutionRefsAreDistinct(t *testing.T) {
	s := newOutputStore(t.TempDir())
	const key = "samekey00"

	ref1, err := s.persist(key, []journal.Event{out("run 1")}, result("p", "build", false, 1))
	require.NoError(t, err)
	ref2, err := s.persist(key, []journal.Event{out("run 2")}, result("p", "build", false, 2))
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
		ref, err := s.persist(key, []journal.Event{out("run")}, result("p", "build", false, int64(i)))
		require.NoError(t, err)
		last = ref
	}

	files, err := os.ReadDir(filepath.Join(dir, "outputs", key))
	require.NoError(t, err)
	assert.Len(t, files, defaultOutputKeepLast, "retention keeps exactly K executions")
	_, _, err = LookupOutput(dir, last)
	assert.NoError(t, err, "the newest execution survives pruning")
}

// TestOutputStorePrefixAndAmbiguity covers git-style prefix resolution.
func TestOutputStorePrefixAndAmbiguity(t *testing.T) {
	dir := t.TempDir()
	s := newOutputStore(dir)

	ref, err := s.persist("k1", []journal.Event{out("body")}, result("p", "build", false, 1))
	require.NoError(t, err)
	data, _, err := LookupOutput(dir, ref)
	require.NoError(t, err)
	assert.Equal(t, "body\n", string(data))

	_, err = s.persist("k2", []journal.Event{out("other")}, result("p", "build", false, 2))
	require.NoError(t, err)
	_, _, err = LookupOutput(dir, RefPrefix) // the bare prefix matches both
	var amb *AmbiguousRefError
	require.True(t, errors.As(err, &amb), "a shared prefix should return *AmbiguousRefError, got %v", err)
	assert.Len(t, amb.Candidates, 2)

	_, _, err = LookupOutput(dir, "refffffffff")
	assert.ErrorIs(t, err, fs.ErrNotExist)
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
	data, meta, err := LookupOutput(cdir, passRef)
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
	fdata, fmeta, err := LookupOutput(cdir, failRef)
	require.NoError(t, err)
	assert.Contains(t, string(fdata), "FAIL: assertion failed")
	assert.True(t, fmeta.Failed)
	assert.Equal(t, "exit status 1", fmeta.Err)
}

// TestOutputStoreRemoveForProject wipes one project's executions while leaving others.
func TestOutputStoreRemoveForProject(t *testing.T) {
	dir := t.TempDir()
	s := newOutputStore(dir)

	keep, err := s.persist("ka", []journal.Event{out("a")}, result("keep/me", "build", false, 1))
	require.NoError(t, err)
	gone, err := s.persist("kb", []journal.Event{out("b")}, result("drop/me", "build", false, 2))
	require.NoError(t, err)

	s.removeForProject("drop/me")

	_, _, err = LookupOutput(dir, gone)
	assert.ErrorIs(t, err, fs.ErrNotExist, "dropped project's execution should be gone")
	_, _, err = LookupOutput(dir, keep)
	assert.NoError(t, err, "other project's execution should remain")

	_, statErr := os.Stat(filepath.Join(dir, "outputs", "kb"))
	assert.ErrorIs(t, statErr, fs.ErrNotExist)
}
