package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/egladman/magus/internal/journal"
	json "github.com/egladman/magus/internal/json"
	runPkg "github.com/egladman/magus/internal/proc/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mustPersist stores one execution and returns its step ref, failing the test on error.
// Most tests only need the ref; the stamped descriptor is re-read via ByRef/Attempts.
func mustPersist(t *testing.T, s *OutputStore, cacheKey string, output []byte, d OutputDescriptor) string {
	t.Helper()
	stored, err := s.Persist(context.Background(), cacheKey, output, d)
	require.NoError(t, err)
	return stored.Ref
}

// TestOutputStorePersistLookupRoundTrip persists one execution's records and reads its
// reconstructed text and derived metadata back by ref.
func TestOutputStorePersistLookupRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)

	desc0 := OutputDescriptor{Project: "svc/api", Target: "test", Failed: true, ErrMsg: "boom", TimestampMs: 1_700_000_000_000, DurationMs: 1200}
	ref := mustPersist(t, s, "deadbeefcafef00d", []byte("lint: undefined symbol foo\n"), desc0)
	assert.Equal(t, RefPrefix+"deadbeefcafe", ref, "the ref is the key's first refHexLen hex digits - portable by construction")

	data, desc, err := s.ByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, "lint: undefined symbol foo\n", string(data), "output is returned verbatim from the blob")

	require.True(t, strings.HasPrefix(desc.Attempt, RefPrefix))
	require.Len(t, desc.Attempt, len(RefPrefix)+attemptHexLen)
	assert.Equal(t, OutputDescriptor{
		Ref: ref, Project: "svc/api", Target: "test",
		Failed: true, ErrMsg: "boom", TimestampMs: 1_700_000_000_000, DurationMs: 1200,
		Key: "deadbeefcafef00d", KeyVersion: KeyVersion,
		Attempt: desc.Attempt,
	}, desc)
}

// TestOutputStorePersistNeverExposesPartialBlob verifies a reader racing Persist never
// observes a half-written .out blob. newestAttemptBlob falls back to modtime for a
// descriptor-less (orphan) blob - exactly the state a fresh Persist is in while its
// write is in flight - so a concurrent reader that resolves through it and reads the
// file must see either nothing yet or the full content, never a short read. Before the
// fix, Persist wrote the blob in place with plain os.WriteFile: the file existed (named,
// zero or partial length) as soon as it was created, well before the write completed.
func TestOutputStorePersistNeverExposesPartialBlob(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)
	const cacheKey = "deadbeefpartial1"

	// Large enough that the write is not a single instantaneous syscall, so a tight
	// polling reader has a real chance of observing an in-progress file.
	payload := bytes.Repeat([]byte("x"), 256<<20) // 256 MiB

	blobDir := filepath.Join(s.outputsDir(), cacheKey)

	var wg sync.WaitGroup
	done := make(chan struct{})
	var shortReads atomic.Int64

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			p := newestAttemptBlob(blobDir)
			if p == "" {
				continue
			}
			// Stat, not a full read: a plain-write blob is visible under its final
			// name (and reported by newestAttemptBlob) well before its content is
			// complete, so an incomplete size alone already proves a concurrent
			// reader could open it and get back less than the full blob. Checking
			// size instead of slurping the whole 256 MiB keeps the poll loop tight
			// enough to actually land inside the write's in-flight window.
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			if info.Size() != int64(len(payload)) {
				shortReads.Add(1)
			}
		}
	}()

	_, err := s.Persist(context.Background(), cacheKey, payload, OutputDescriptor{Project: "svc/api", Target: "test"})
	require.NoError(t, err)
	close(done)
	wg.Wait()

	assert.Zero(t, shortReads.Load(), "a concurrent reader must never see a partially-written .out blob")
}

// TestOutputStorePersistRevisionRoundTrip pins schema v3: Revision and Dirty persist and
// read back like every other descriptor field, via the same Persist -> ByRef path
// TestOutputStorePersistLookupRoundTrip covers for the pre-v3 fields.
func TestOutputStorePersistRevisionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)

	desc0 := OutputDescriptor{
		Project: "svc/api", Target: "build", TimestampMs: 1_700_000_000_000, DurationMs: 500,
		Revision: "abcdef0123456789abcdef0123456789abcdef01", Dirty: true,
	}
	ref := mustPersist(t, s, "cafebabecafebabe", []byte("built\n"), desc0)

	_, desc, err := s.ByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, OutputDescriptor{
		Ref: ref, Project: "svc/api", Target: "build",
		TimestampMs: 1_700_000_000_000, DurationMs: 500,
		Revision: "abcdef0123456789abcdef0123456789abcdef01", Dirty: true,
		Key: "cafebabecafebabe", KeyVersion: KeyVersion,
		Attempt: desc.Attempt,
	}, desc)
}

// TestOutputStoreV2DescriptorResolvesWithEmptyRevision pins backward compatibility for
// schema v3: a v2 descriptor (portable refs, but written before Revision/Dirty existed)
// keeps resolving, and reads back with an empty Revision - "unknown", not an error.
// Mirrors TestPrePortableStoreResolves, one schema generation later.
func TestOutputStoreV2DescriptorResolvesWithEmptyRevision(t *testing.T) {
	dir := t.TempDir()
	const key = "2222222222222222222222222222222222222222222222222222222222222222"
	keyDir := filepath.Join(dir, "outputs", key)
	require.NoError(t, os.MkdirAll(keyDir, 0o755))
	const attempt = "out22222222" // shaped like a real attempt id
	v2 := []byte(`{"ref":"` + PortableRef(key) + `","project":"pkg/a","target":"build","failed":false,"timestamp_ms":100,"duration_ms":5,` +
		`"schema":2,"key":"` + key + `","key_version":1,"attempt":"` + attempt + `"}`)
	require.NoError(t, os.WriteFile(filepath.Join(keyDir, attempt+outExt), []byte("v2 bytes\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(keyDir, attempt+descExt), v2, 0o644))

	s := NewOutputStore(dir)
	data, desc, err := s.ByRef(PortableRef(key))
	require.NoError(t, err)
	assert.Equal(t, "v2 bytes\n", string(data))
	assert.Empty(t, desc.Revision, "a v2 descriptor predates Revision; it reads as unknown, never an error")
	assert.False(t, desc.Dirty)
}

// TestRotateRunsKeepsNewestAndReportsFreed writes several invocation journals with staggered
// modtimes, then prunes to a cap and checks the newest survive, the oldest are gone, and the
// removed count and freed bytes are reported.
func TestRotateRunsKeepsNewestAndReportsFreed(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)
	runs := filepath.Join(dir, RunsDir)
	require.NoError(t, os.MkdirAll(runs, 0o755))

	base := time.Unix(1_700_000_000, 0)
	body := []byte("{}\n") // 3 bytes each
	for i := 0; i < 5; i++ {
		p := filepath.Join(runs, fmt.Sprintf("inv%d.jsonl", i))
		require.NoError(t, os.WriteFile(p, body, 0o644))
		// inv0 oldest .. inv4 newest.
		require.NoError(t, os.Chtimes(p, base.Add(time.Duration(i)*time.Minute), base.Add(time.Duration(i)*time.Minute)))
	}

	removed, freed := s.RotateRuns(2) // keep inv4, inv3
	require.Equal(t, 3, removed)
	require.Equal(t, int64(3*len(body)), freed)

	got, err := os.ReadDir(runs)
	require.NoError(t, err)
	names := make([]string, 0, len(got))
	for _, e := range got {
		names = append(names, e.Name())
	}
	require.ElementsMatch(t, []string{"inv3.jsonl", "inv4.jsonl"}, names)
}

func TestRotateRunsUnderCapAndZeroAreNoops(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)
	runs := filepath.Join(dir, RunsDir)
	require.NoError(t, os.MkdirAll(runs, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(runs, "inv0.jsonl"), []byte("x\n"), 0o644))

	removed, freed := s.RotateRuns(10) // under cap
	require.Equal(t, 0, removed)
	require.Zero(t, freed)

	removed, _ = s.RotateRuns(0) // never wipe the whole dir
	require.Equal(t, 0, removed)

	got, _ := os.ReadDir(runs)
	require.Len(t, got, 1)
}

func TestRunsStatCountsJournals(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)
	bytes, count := s.RunsStat()
	require.Zero(t, bytes) // missing runs dir is (0, 0)
	require.Zero(t, count)

	runs := filepath.Join(dir, RunsDir)
	require.NoError(t, os.MkdirAll(runs, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(runs, "inv0.jsonl"), []byte("abc\n"), 0o644)) // 4 bytes
	require.NoError(t, os.WriteFile(filepath.Join(runs, "inv1.jsonl"), []byte("de\n"), 0o644))  // 3 bytes
	require.NoError(t, os.WriteFile(filepath.Join(runs, "notes.txt"), []byte("ignored"), 0o644))

	bytes, count = s.RunsStat()
	require.Equal(t, int64(2), count) // only .jsonl journals counted
	require.Equal(t, int64(7), bytes)
}

// TestOutputStoreVerbatimFidelity pins the reason for the blob store: `magus query ref` returns
// the EXACT bytes the process wrote. The old reconstruct-from-line-records path re-added a
// trailing newline to output that had none (printf "done"); the verbatim blob does not.
func TestOutputStoreVerbatimFidelity(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)
	for i, raw := range []string{
		"done",             // no trailing newline
		"a\nb\nc\n",        // trailing newline preserved
		"with\ttabs\r\nCR", // control chars + CRLF, no final newline
		"",                 // empty output
	} {
		// One key per case: a shared key would resolve the STEP (newest attempt),
		// and this test is about byte fidelity, not attempt selection.
		ref := mustPersist(t, s, fmt.Sprintf("k%d", i), []byte(raw), OutputDescriptor{Project: "p", Target: "t"})
		got, _, err := s.ByRef(ref)
		require.NoError(t, err)
		assert.Equal(t, raw, string(got), "output must round-trip byte-for-byte")
	}
}

// TestOutputStoreAttemptsShareOnePortableRef verifies repeated executions of ONE cache
// key share the step's portable ref while staying independently addressable as attempts
// (keep-last-K history): the bare ref answers with the newest bytes, --attempts lists
// every execution newest first, and a full attempt id retrieves that exact execution.
func TestOutputStoreAttemptsShareOnePortableRef(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)
	const key = "aabbccddeeff0011"

	ref1 := mustPersist(t, s, key, []byte("run 1\n"), OutputDescriptor{Project: "p", Target: "build", TimestampMs: 100})
	ref2 := mustPersist(t, s, key, []byte("run 2\n"), OutputDescriptor{Project: "p", Target: "build", TimestampMs: 200})

	assert.Equal(t, ref1, ref2, "every execution of one cache key shares the portable ref")
	assert.Equal(t, ref1, s.StepRef(key))

	attempts, err := s.Attempts(ref1)
	require.NoError(t, err)
	require.Len(t, attempts, 2, "both executions stay addressable")
	assert.True(t, newerDescriptor(attempts[0], attempts[1]), "attempts list newest first")
	assert.NotEqual(t, attempts[0].Attempt, attempts[1].Attempt, "attempt ids are execution-unique")

	// The newest attempt answers for the bare step ref; stagger mtimes so "newest"
	// is unambiguous on coarse-granularity filesystems.
	older := filepath.Join(dir, "outputs", key, attempts[1].Attempt+outExt)
	past := time.Now().Add(-time.Minute)
	require.NoError(t, os.Chtimes(older, past, past))
	data, desc, err := s.ByRef(ref1)
	require.NoError(t, err)
	assert.Equal(t, "run 2\n", string(data))
	assert.Equal(t, int64(200), desc.TimestampMs)

	// A full attempt id retrieves that exact execution, even when it is not the newest.
	data, desc, err = s.ByRef(attempts[1].Attempt)
	require.NoError(t, err)
	assert.Equal(t, "run 1\n", string(data))
	assert.Equal(t, int64(100), desc.TimestampMs)
}

// TestPortableRefDeterministicAcrossCaches is the point of portable refs: two machines
// (modeled as two fresh cache dirs) running the same step over the same workspace
// content print the SAME ref, so an inspect line pasted from CI resolves anywhere and
// differing refs prove differing inputs.
func TestPortableRefDeterministicAcrossCaches(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test", "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test", "pkg", "main.go"), []byte("package main"), 0o644))

	runOnce := func(t *testing.T) string {
		t.Helper()
		c, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"), WithMutable(true))
		require.NoError(t, err)
		r, err := c.Run(context.Background(), makeStep(root), func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			fmt.Fprintln(stdout, "built")
			return nil
		})
		require.NoError(t, err)
		require.NotEmpty(t, r.Ref)
		return r.Ref
	}

	refA := runOnce(t)
	refB := runOnce(t)
	assert.Equal(t, refA, refB, "same inputs must mint the same ref in two independent caches")
	assert.Len(t, refA, len(RefPrefix)+refHexLen)
}

// TestVolatileFailuresAccumulateAttemptsUnderOneRef drives the real Run path with a
// target that fails on every execution: all three failures share one portable ref,
// stay independently retrievable as attempts, and the bare ref answers with the
// newest. This is the property the old per-execution nonce protected, relocated a
// level down.
func TestVolatileFailuresAccumulateAttemptsUnderOneRef(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	step := makeStep(root)
	step.Target = "flaky"

	boom := errors.New("exit status 1")
	var refs []string
	for i := 0; i < 3; i++ {
		_, err := c.Run(context.Background(), step, func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			fmt.Fprintf(stdout, "attempt %d failed\n", i)
			return boom
		})
		require.ErrorIs(t, err, boom)
		hash, herr := c.hashStep(context.Background(), &step)
		require.NoError(t, herr)
		refs = append(refs, c.outputs.StepRef(hash))
	}
	assert.Equal(t, refs[0], refs[1])
	assert.Equal(t, refs[1], refs[2], "every failing execution shares the step's portable ref")

	attempts, err := c.outputs.Attempts(refs[0])
	require.NoError(t, err)
	require.Len(t, attempts, 3, "each failure stays independently addressable")
	for _, a := range attempts {
		assert.True(t, a.Failed)
		data, _, err := c.outputs.ByRef(a.Attempt)
		require.NoError(t, err)
		assert.Contains(t, string(data), "failed")
	}
}

// TestPrePortableStoreResolves pins backward compatibility: a store written before
// portable refs (execution-unique 8-hex file stems, v1 descriptors with no schema/key
// fields) keeps resolving - by its old ref exactly, and at the step level once the key
// directory is addressed - and Attempts backfills the attempt id from the file stem.
func TestPrePortableStoreResolves(t *testing.T) {
	dir := t.TempDir()
	const key = "0123456789abcdef0123456789abcdef"
	keyDir := filepath.Join(dir, "outputs", key)
	require.NoError(t, os.MkdirAll(keyDir, 0o755))
	const oldRef = "out1a2b3c4d" // v1 shape: RefPrefix + 8 hex, minted per execution
	require.NoError(t, os.WriteFile(filepath.Join(keyDir, oldRef+outExt), []byte("legacy bytes\n"), 0o644))
	v1 := []byte(`{"ref":"` + oldRef + `","project":"pkg/a","target":"build","failed":false,"timestamp_ms":100,"duration_ms":5}`)
	require.NoError(t, os.WriteFile(filepath.Join(keyDir, oldRef+descExt), v1, 0o644))

	s := NewOutputStore(dir)

	data, desc, err := s.ByRef(oldRef) // the exact ref a v1 run printed
	require.NoError(t, err)
	assert.Equal(t, "legacy bytes\n", string(data))
	assert.Equal(t, oldRef, desc.Ref)

	data, _, err = s.ByRef(PortableRef(key)) // the step-level ref the same key mints today
	require.NoError(t, err)
	assert.Equal(t, "legacy bytes\n", string(data))

	assert.Equal(t, PortableRef(key), s.StepRef(key), "a pre-portable dir already answers with the portable ref")

	attempts, err := s.Attempts(oldRef)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, oldRef, attempts[0].Attempt, "the v1 file stem backfills the attempt id")
}

// TestLatestRefsByTarget: the newest execution per (project, target) is returned, keyed
// by descriptor timestamp; the charm suffix reproTarget stores ("build:rw") is collapsed
// to the bare declared target, so a target's newest run is picked across its charm
// variants; project-scoped outputs (no target) are skipped; the result is sorted by
// project then bare target.
func TestLatestRefsByTarget(t *testing.T) {
	s := NewOutputStore(t.TempDir())

	// pkg/a:build ran twice under different charms; the later timestamp wins even though
	// the two carry distinct repro targets ("build:ro" then "build:rw").
	older := mustPersist(t, s, "ka1", []byte("old\n"), OutputDescriptor{Project: "pkg/a", Target: "build:ro", TimestampMs: 100, Failed: true})
	newer := mustPersist(t, s, "ka2", []byte("new\n"), OutputDescriptor{Project: "pkg/a", Target: "build:rw", TimestampMs: 200, Failed: false})
	// A different target, and a project-scoped output that must be skipped.
	testRef := mustPersist(t, s, "kb", []byte("t\n"), OutputDescriptor{Project: "pkg/a", Target: "test", TimestampMs: 150})
	mustPersist(t, s, "kc", []byte("proj\n"), OutputDescriptor{Project: "pkg/a", TimestampMs: 999}) // no target -> skipped

	got := s.LatestRefsByTarget()
	require.Len(t, got, 2, "one entry per (project, bare target); charm variants collapse, the target-less run is skipped")

	assert.Equal(t, "pkg/a", got[0].Project)
	assert.Equal(t, "build", got[0].Target, "charm suffix stripped; sorted build before test")
	assert.Equal(t, newer, got[0].Ref, "the newer timestamp wins across charm variants")
	assert.False(t, got[0].Failed, "the winning run's outcome rides along")
	assert.NotEqual(t, older, got[0].Ref)

	assert.Equal(t, "test", got[1].Target)
	assert.Equal(t, testRef, got[1].Ref)
}

// TestLatestRefsByTargetEmpty: an output store with nothing persisted returns no refs
// (nil), so the graph builder simply omits the last-output attrs.
func TestLatestRefsByTargetEmpty(t *testing.T) {
	assert.Nil(t, NewOutputStore(t.TempDir()).LatestRefsByTarget())
}

// TestListDescriptors: unlike LatestRefsByTarget, the browser feed keeps EVERY retained execution
// (not just the newest per target) and includes target-less project-scoped runs, newest first.
func TestListDescriptors(t *testing.T) {
	s := NewOutputStore(t.TempDir())
	// Two executions of the same target under one cache key (both retained by keep-last-K), a second
	// target, and a target-less project-scoped run - all four must appear.
	r1 := mustPersist(t, s, "k1", []byte("old build\n"), OutputDescriptor{Project: "pkg/a", Target: "build:rw", TimestampMs: 100})
	r2 := mustPersist(t, s, "k1", []byte("new build\n"), OutputDescriptor{Project: "pkg/a", Target: "build:rw", TimestampMs: 300})
	r3 := mustPersist(t, s, "k2", []byte("test\n"), OutputDescriptor{Project: "pkg/a", Target: "test", TimestampMs: 200})
	r4 := mustPersist(t, s, "k3", []byte("scope\n"), OutputDescriptor{Project: "pkg/b", TimestampMs: 400}) // no target: still listed

	got := s.ListDescriptors()
	require.Len(t, got, 4, "every retained execution is listed, including the target-less project-scoped run")
	// Newest first by timestamp: r4 (400), r2 (300), r3 (200), r1 (100).
	assert.Equal(t, []string{r4, r2, r3, r1}, []string{got[0].Ref, got[1].Ref, got[2].Ref, got[3].Ref})
	assert.Equal(t, "build:rw", got[1].Target, "the repro target (charm suffix) is preserved verbatim")
}

// TestListDescriptorsEmpty: a store with nothing persisted returns nil, so the browser shows an
// empty tree rather than erroring.
func TestListDescriptorsEmpty(t *testing.T) {
	assert.Nil(t, NewOutputStore(t.TempDir()).ListDescriptors())
}

// TestOutputStoreKeepLastK bounds retention to defaultOutputKeepLast newest executions
// per cache key; the newest survives.
func TestOutputStoreKeepLastK(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)
	const key = "boundedkey"

	var last string
	for i := 0; i < defaultOutputKeepLast+3; i++ {
		ref := mustPersist(t, s, key, []byte("run\n"), OutputDescriptor{Project: "p", Target: "build"})
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

	ref := mustPersist(t, s, "k1", []byte("body\n"), OutputDescriptor{Project: "p", Target: "build"})
	data, _, err := s.ByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, "body\n", string(data))

	mustPersist(t, s, "k2", []byte("other\n"), OutputDescriptor{Project: "p", Target: "build"})
	_, _, err = s.ByRef(RefPrefix) // the bare prefix matches both
	var amb *AmbiguousRefError
	require.True(t, errors.As(err, &amb), "a shared prefix should return *AmbiguousRefError, got %v", err)
	assert.Len(t, amb.Candidates, 2)

	_, _, err = s.ByRef("outffffffff")
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
	require.NoError(t, enc.Encode(journal.Event{Kind: journal.KindStarted, Command: &journal.Command{Arguments: []string{"run", "build"}, Trigger: "agent"}}))
	require.NoError(t, enc.Encode(journal.Event{Kind: journal.KindFinished, Status: journal.StatusPass}))
	require.NoError(t, f.Close())

	inv, err := NewOutputStore(dir).InvocationByID("inv123")
	require.NoError(t, err)
	assert.Equal(t, "inv123", inv.ID)
	assert.Equal(t, []string{"run", "build"}, inv.Command.Arguments)

	_, err = NewOutputStore(dir).InvocationByID("missing")
	assert.ErrorIs(t, err, fs.ErrNotExist, "an aged-out run log surfaces as fs.ErrNotExist")
}

// TestInvocationEventsByID pins the read side of the audit trail: the EVENTS survive, not
// just the header InvocationByID reconstructs from them. journal.KindSecret is the reason -
// a run's credential reads were recorded and then unreachable, so the trail had no reader.
func TestInvocationEventsByID(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, RunsDir)
	require.NoError(t, os.MkdirAll(runs, 0o755))
	f, err := os.Create(filepath.Join(runs, "invaudit1.jsonl"))
	require.NoError(t, err)
	enc := json.NewEncoder(f)
	require.NoError(t, enc.Encode(journal.Event{Kind: journal.KindStarted, Command: &journal.Command{Arguments: []string{"run", "image-login"}}}))
	require.NoError(t, enc.Encode(journal.Event{Kind: journal.KindSecret, Project: ".", Target: "image-login", Text: `read secret "GHCR_TOKEN" via onepassword`}))
	require.NoError(t, enc.Encode(journal.Event{Kind: journal.KindFinished, Status: journal.StatusPass}))
	require.NoError(t, f.Close())

	header, events, err := NewOutputStore(dir).InvocationEventsByID("invaudit1")
	require.NoError(t, err)
	assert.Equal(t, "invaudit1", header.ID)
	assert.Equal(t, journal.StatusPass, header.Status, "the header is still reconstructed")
	require.Len(t, events, 3, "every event is returned, not only the two lifecycle ones")

	var secrets []journal.Event
	for _, e := range events {
		if e.Kind == journal.KindSecret {
			secrets = append(secrets, e)
		}
	}
	require.Len(t, secrets, 1, "the credential read is reachable")
	assert.Contains(t, secrets[0].Text, "GHCR_TOKEN", "the reference is the auditable fact")
	assert.Contains(t, secrets[0].Text, "onepassword", "so is the provider that served it")

	_, _, err = NewOutputStore(dir).InvocationEventsByID("invmissing")
	assert.ErrorIs(t, err, fs.ErrNotExist, "an aged-out run log surfaces as fs.ErrNotExist")
}

// TestListRunLogsReadsHeadAndTailOnly is the run browser's feed: the invocation list has to come off
// bounded reads, because a journal holds every output line its run captured and listing hundreds of
// them by parsing each in full would read hundreds of megabytes to paint a sidebar. The padding here
// is what makes that observable - it is larger than the tail window, so a whole-file read would be
// the only way to reach the started event from the end, and a whole-file parse the only way to reach
// the finished event from the start.
func TestListRunLogsReadsHeadAndTailOnly(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, RunsDir)
	require.NoError(t, os.MkdirAll(runs, 0o755))

	write := func(name string, evs ...journal.Event) string {
		p := filepath.Join(runs, name+".jsonl")
		f, err := os.Create(p)
		require.NoError(t, err)
		enc := json.NewEncoder(f)
		for _, e := range evs {
			require.NoError(t, enc.Encode(e))
		}
		require.NoError(t, f.Close())
		return p
	}
	padding := make([]journal.Event, 0, 400)
	for i := 0; i < 400; i++ {
		padding = append(padding, journal.Event{Kind: journal.KindOutput, Ts: 200, Text: strings.Repeat("x", 400)})
	}

	sweep := append([]journal.Event{
		{Kind: journal.KindStarted, Ts: 100, MagusVersion: "v9", Command: &journal.Command{Arguments: []string{"affected", "ci"}, Trigger: journal.TriggerCI}},
	}, padding...)
	sweep = append(sweep, journal.Event{Kind: journal.KindFinished, Ts: 900, Status: journal.StatusFail})
	sweepPath := write("invsweep", sweep...)
	require.Greater(t, fileSize(t, sweepPath), int64(runTailWindow), "the padding must exceed the tail window")

	// An interrupted run: a started event and output, but no finished event to read an outcome from.
	killedPath := write("invkilled",
		journal.Event{Kind: journal.KindStarted, Ts: 10, Command: &journal.Command{Arguments: []string{"run", "build"}, Trigger: journal.TriggerRun}},
		journal.Event{Kind: journal.KindOutput, Ts: 40, Text: "compiling"},
	)
	require.NoError(t, os.WriteFile(filepath.Join(runs, "notes.txt"), []byte("ignored"), 0o644))

	base := time.Unix(1_700_000_000, 0)
	require.NoError(t, os.Chtimes(killedPath, base, base))
	require.NoError(t, os.Chtimes(sweepPath, base.Add(time.Minute), base.Add(time.Minute)))

	got := NewOutputStore(dir).ListRunLogs(0)
	require.Len(t, got, 2, "only .jsonl journals list")
	assert.Equal(t, "invsweep", got[0].Inv, "newest by modtime first")
	assert.Equal(t, []string{"affected", "ci"}, got[0].Arguments)
	assert.Equal(t, journal.TriggerCI, got[0].Trigger)
	assert.Equal(t, int64(100), got[0].StartedMs)
	assert.Equal(t, int64(900), got[0].FinishedMs)
	assert.Equal(t, journal.StatusFail, got[0].Status)
	assert.Equal(t, "v9", got[0].MagusVersion)

	assert.Equal(t, "invkilled", got[1].Inv)
	assert.Empty(t, got[1].Status, "no finished event means no outcome to claim")
	assert.Equal(t, int64(40), got[1].FinishedMs, "the last event still dates the run")

	assert.Len(t, NewOutputStore(dir).ListRunLogs(1), 1, "a positive limit keeps the newest")
	assert.Empty(t, NewOutputStore(t.TempDir()).ListRunLogs(0), "a store with no runs dir lists nothing")
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.Size()
}

// A journal whose first line is not a started event has no command lineage to report, but it is
// still a run that happened - listing it without an argv beats hiding it.
func TestListRunLogsKeepsAJournalWithNoStartedEvent(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, RunsDir)
	require.NoError(t, os.MkdirAll(runs, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(runs, "invodd.jsonl"), []byte("not json\n"), 0o644))

	got := NewOutputStore(dir).ListRunLogs(0)
	require.Len(t, got, 1)
	assert.Equal(t, "invodd", got[0].Inv)
	assert.Empty(t, got[0].Arguments)
	assert.Zero(t, got[0].StartedMs)
}

// TestLooksLikeInvocationID pins the recognizer that keeps a pasted invocation id out of the
// graph grammar, where it matched nothing and reported `matches: 0` - which reads as "no such
// run" rather than "wrong command".
func TestLooksLikeInvocationID(t *testing.T) {
	for _, s := range []string{"invmsm3vcou1", "inv123", "invabc0z9"} {
		assert.True(t, LooksLikeInvocationID(s), "%q should be recognized as an invocation id", s)
	}
	// "invoke"/"invalid" are the collisions that matter: they are plausible free-text search
	// terms, and stealing them from the graph grammar would be a regression in itself. They
	// are lowercase alphanumeric, so they DO match the shape - the router only reaches this
	// check for a single positional, and a miss reports a missing run log rather than
	// searching. Pin the shapes that must never match at all.
	for _, s := range []string{"inv", "INV123", "inv-123", "inv 123", "kind:spell", "out1a2b3c", ""} {
		assert.False(t, LooksLikeInvocationID(s), "%q must NOT be treated as an invocation id", s)
	}
}

// TestAmbiguousRefErrorMessage covers AmbiguousRefError.Error's rendering.
func TestAmbiguousRefErrorMessage(t *testing.T) {
	e := &AmbiguousRefError{Prefix: "refde", Candidates: []string{"outdead", "outdeed"}}
	msg := e.Error()
	assert.Contains(t, msg, "refde")
	assert.Contains(t, msg, "ambiguous")
	assert.Contains(t, msg, "outdead")
	assert.Contains(t, msg, "outdeed")
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

	ref := c.outputs.StepRef(r.Hash)
	require.NotEmpty(t, ref)
	data, _, err := c.outputs.ByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, "no newline here", string(data), "verbatim - no newline invented")
}

// TestLooksLikeRef pins the query router's discriminator.
func TestLooksLikeRef(t *testing.T) {
	for _, s := range []string{"out1a2b3c", "outdeadbeef", "outa", "out0"} {
		assert.True(t, LooksLikeRef(s), "%q should be recognized as a ref", s)
	}
	for _, s := range []string{"output", "outer", "out", "out ", "kind:spell", "OUT1A2B", "1a2b3c", ""} {
		assert.False(t, LooksLikeRef(s), "%q must NOT be treated as a ref", s)
	}
}

// TestIsMintedRef pins the exact-length ref shapes used to scan free text: ref + exactly
// refHexLen hex (a portable step ref) or attemptHexLen hex (an attempt id / pre-portable
// ref) is accepted, so prefixes and coincidentally-hex words are rejected.
func TestIsMintedRef(t *testing.T) {
	for _, s := range []string{"out1a2b3c4d", "outdeadbeef", "out1a2b3c4d5e6f", "outdeadbeefcafe"} {
		assert.True(t, IsMintedRef(s), "%q should be a minted ref", s)
	}
	for _, s := range []string{"outace", "outed", "out1a2b3c", "outa", "out", "output", "out1a2b3c4d5", ""} {
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

	passRef := c.outputs.StepRef(rPass.Hash)
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
	failRef := c.outputs.StepRef(failHash)
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

	keep := mustPersist(t, s, "ka", []byte("a\n"), OutputDescriptor{Project: "keep/me", Target: "build"})
	gone := mustPersist(t, s, "kb", []byte("b\n"), OutputDescriptor{Project: "drop/me", Target: "build"})

	s.removeForProject("drop/me")

	_, _, err := s.ByRef(gone)
	assert.ErrorIs(t, err, fs.ErrNotExist, "dropped project's execution should be gone")
	_, _, err = s.ByRef(keep)
	assert.NoError(t, err, "other project's execution should remain")

	_, statErr := os.Stat(filepath.Join(dir, "outputs", "kb"))
	assert.ErrorIs(t, statErr, fs.ErrNotExist)
}

// These benchmarks baseline the output-store paths that `magus query ref` rides. Before the
// verbatim-blob refactor the store JSON-encoded per-line events on write and RECONSTRUCTED raw
// text on read; after, persist writes the raw blob + one descriptor and OutputByRef is a
// straight file read. Same benchmark names bracket both, so `benchstat old new` quantifies the
// win (go test -bench=OutputStore -benchmem -count=10).

// benchRaw builds a realistic target log: n lines (~80 bytes each) as verbatim bytes.
func benchRaw(n int) []byte {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "[%04d] go: downloading example.com/some/module v1.%d.0 (cached, verified)\n", i, i%9)
	}
	return []byte(b.String())
}

const benchLines = 200

func benchMeta() OutputDescriptor {
	return OutputDescriptor{Project: "cmd/magus", Target: "build", DurationMs: 1234}
}

// BenchmarkOutputStorePersist measures the write path run for every cached target execution.
func BenchmarkOutputStorePersist(b *testing.B) {
	raw := benchRaw(benchLines)
	meta := benchMeta()
	s := NewOutputStore(b.TempDir())
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if _, err := s.Persist(context.Background(), fmt.Sprintf("deadbeefcafef%03d", i%1000), raw, meta); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOutputStoreLookupOutput measures the full `magus query output <ref>` read.
func BenchmarkOutputStoreLookupOutput(b *testing.B) {
	raw := benchRaw(benchLines)
	dir := b.TempDir()
	s := NewOutputStore(dir)
	stored, err := s.Persist(context.Background(), "deadbeefcafef00d", raw, benchMeta())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := s.ByRef(stored.Ref); err != nil {
			b.Fatal(err)
		}
	}
}

// The contracts in this file are the ones portable refs REST ON. Each is cheap to
// break by accident from a long way away (the hit path, the resolver, a shape
// constant, a consumer's regexp) and expensive to notice, because the system keeps
// working locally - refs simply stop meaning the same thing on two machines. Line
// coverage does not protect any of them; these assertions do.

// realRefShape is what a run actually prints: the prefix plus exactly refHexLen hex.
var realRefShape = regexp.MustCompile(`^out[0-9a-f]{12}$`)

// TestRefShapeContract pins the WIRE SHAPE of both id kinds, because they are pasted
// into terminals, matched by consumers' own patterns (the MCP hint scanner), and
// quoted in docs. Changing a length is legitimate; changing it ACCIDENTALLY is what
// this catches. It also pins that both shapes satisfy the two public predicates, so a
// consumer scanning free text keeps recognizing real ids.
func TestRefShapeContract(t *testing.T) {
	assert.Equal(t, "out", RefPrefix)
	assert.Equal(t, 12, refHexLen, "a ref is out + 12 hex; changing this changes every printed id")
	assert.Equal(t, 8, attemptHexLen, "an attempt keeps the pre-portable width, so old ids still parse")

	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	step := makeStep(root)
	step.Target = "build"
	r, err := c.Run(context.Background(), step, func(context.Context) error { return nil })
	require.NoError(t, err)

	require.Regexp(t, realRefShape, r.Ref, "a real run must print out + 12 hex")
	assert.Len(t, r.Ref, 15, "callers size terminal output and test fixtures against this")
	assert.True(t, LooksLikeRef(r.Ref), "`magus query output` must accept what a run printed")
	assert.True(t, IsMintedRef(r.Ref), "a free-text scanner must recognize what a run printed")

	attempts, err := c.outputs.Attempts(r.Ref)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.True(t, IsMintedRef(attempts[0].Attempt), "an attempt id must also be recognizable")
	assert.True(t, LooksLikeRef(attempts[0].Attempt))
}

// TestCacheKeyUnaffectedByPlatform pins that Manifest.Platform (the replay-time
// gate added alongside portable refs) is NOT a key input. The key is what an
// output ref truncates, and a ref must be identical on every machine - that is
// the whole portable-ref feature - so platform must only ever be consulted after
// the key is computed, never folded into hashStepInputs. Two Cache instances that
// differ ONLY in platform must hash the same step to the same key.
func TestCacheKeyUnaffectedByPlatform(t *testing.T) {
	root := t.TempDir()
	writeMain(t, root, "package main")
	step := makeStep(root)
	step.Target = "build"

	cLinux, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"), withPlatform("linux/amd64"))
	require.NoError(t, err)
	cDarwin, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"), withPlatform("darwin/arm64"))
	require.NoError(t, err)

	hLinux, err := cLinux.hashStep(context.Background(), &step)
	require.NoError(t, err)
	hDarwin, err := cDarwin.hashStep(context.Background(), &step)
	require.NoError(t, err)

	assert.Equal(t, hLinux, hDarwin, "cache key must be byte-identical across platforms")
}

// TestCacheHitReusesTheSameRef is THE portability contract, and the easiest one to
// break from far away: a hit must answer with the ref the miss printed. If a hit ever
// minted a fresh id, every existing test would still pass while two machines - or the
// same machine twice - silently stopped agreeing on what a run is called.
func TestCacheHitReusesTheSameRef(t *testing.T) {
	root, cdir, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}
	build := func(context.Context) error { return os.WriteFile(out, []byte("built"), 0o644) }

	miss, err := c.Run(context.Background(), step, build)
	require.NoError(t, err)
	require.False(t, miss.Hit)
	require.NotEmpty(t, miss.Ref)

	c2, err := Open(t.Context(), cdir, WithMutable(false))
	require.NoError(t, err)
	hit, err := c2.Run(context.Background(), step, build)
	require.NoError(t, err)
	require.True(t, hit.Hit, "second run must hit")
	assert.Equal(t, miss.Ref, hit.Ref, "a hit must reuse the miss's ref, never mint a new one")

	// And the ref still resolves to the captured bytes after the hit.
	_, desc, err := c2.outputs.ByRef(hit.Ref)
	require.NoError(t, err)
	assert.Equal(t, "test/pkg", desc.Project)
}

// TestFailingRunRefResolves: failures are the outputs people most want to retrieve,
// and they take a different code path (never snapshotted, never pushed). The ref a
// failing run prints must resolve to that run's bytes.
func TestFailingRunRefResolves(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	step := makeStep(root)
	step.Target = "test"

	boom := errors.New("exit status 1")
	res, err := c.Run(context.Background(), step, func(ctx context.Context) error {
		stdout, _ := runPkg.OutputWriters(ctx)
		fmt.Fprintln(stdout, "FAIL: one assertion")
		return boom
	})
	require.ErrorIs(t, err, boom)
	require.Regexp(t, realRefShape, res.Ref, "a failing run prints a real ref too")

	data, desc, err := c.outputs.ByRef(res.Ref)
	require.NoError(t, err)
	assert.Contains(t, string(data), "FAIL: one assertion")
	assert.True(t, desc.Failed)
}

// TestConcurrentPersistsShareRefWithDistinctAttempts: the store advertises safety for
// concurrent Persist. Two executions of one step must agree on the ref and disagree on
// the attempt - if attempts ever collided, one run's output would overwrite another's.
func TestConcurrentPersistsShareRefWithDistinctAttempts(t *testing.T) {
	s := NewOutputStore(t.TempDir())
	const key = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	const n = 8

	var wg sync.WaitGroup
	refs := make([]string, n)
	attempts := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stored, err := s.Persist(context.Background(), key,
				[]byte(fmt.Sprintf("run %d\n", i)), OutputDescriptor{Project: "p", Target: "build"})
			if err == nil {
				refs[i], attempts[i] = stored.Ref, stored.Attempt
			}
		}()
	}
	wg.Wait()

	seen := map[string]struct{}{}
	for i := range n {
		require.Equal(t, PortableRef(key), refs[i], "every concurrent execution shares the step's ref")
		require.NotEmpty(t, attempts[i])
		_, dup := seen[attempts[i]]
		require.False(t, dup, "attempt ids collided: one run would overwrite another")
		seen[attempts[i]] = struct{}{}
	}
}

// TestOldRefStillResolvesAfterUpgrade: a ref copied from scrollback, a ticket, or a CI
// log written by the PREVIOUS magus is 11 chars. Upgrading must not turn those into
// dead ids while the outputs are still on disk.
func TestOldRefStillResolvesAfterUpgrade(t *testing.T) {
	dir := t.TempDir()
	const key = "9988776655443322119900aabbccddee"
	keyDir := filepath.Join(dir, "outputs", key)
	require.NoError(t, os.MkdirAll(keyDir, 0o755))
	const oldRef = "outfeedface" // out + 8 hex, exactly what a pre-portable run printed
	require.NoError(t, os.WriteFile(filepath.Join(keyDir, oldRef+outExt), []byte("legacy output\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(keyDir, oldRef+descExt),
		[]byte(`{"ref":"`+oldRef+`","project":"pkg/a","target":"build","failed":false,"timestamp_ms":1,"duration_ms":1}`), 0o644))

	s := NewOutputStore(dir)
	data, _, err := s.ByRef(oldRef)
	require.NoError(t, err, "an id printed by the previous magus must still resolve")
	assert.Equal(t, "legacy output\n", string(data))

	// A git-style short prefix of the old id keeps working too.
	data, _, err = s.ByRef("outfeed")
	require.NoError(t, err)
	assert.Equal(t, "legacy output\n", string(data))
}

// A 12-hex ref is 48 bits, and the design ACCEPTS a birthday collision at roughly
// 1e-3 per million distinct steps rather than paying for a longer id. That trade is
// only defensible if colliding refs stay RECOVERABLE, which is the one path a user
// hits precisely when they are already confused. These tests prove the recovery
// works rather than trusting the comment that says it does.

// TestCollidingRefsListDistinguishableCandidates: two cache keys sharing their first
// refHexLen hex render the SAME portable ref. Asking for it must not dead-end - the
// ambiguity error has to name candidates that are distinct AND that actually resolve,
// or the user has no way back (they cannot see the full keys to lengthen the prefix
// themselves).
func TestCollidingRefsListDistinguishableCandidates(t *testing.T) {
	s := NewOutputStore(t.TempDir())
	// Identical for the first 12 hex, different immediately after.
	const keyA = "abcdef012345" + "aaaa000000000000000000000000000000000000000000000000"
	const keyB = "abcdef012345" + "bbbb000000000000000000000000000000000000000000000000"
	require.Equal(t, PortableRef(keyA), PortableRef(keyB), "the fixture must actually collide")

	mustPersist(t, s, keyA, []byte("output from A\n"), OutputDescriptor{Project: "pkg/a", Target: "build"})
	mustPersist(t, s, keyB, []byte("output from B\n"), OutputDescriptor{Project: "pkg/b", Target: "build"})

	_, _, err := s.ByRef(PortableRef(keyA))
	var amb *AmbiguousRefError
	require.True(t, errors.As(err, &amb), "a collided ref must report ambiguity, got %v", err)
	require.Len(t, amb.Candidates, 2)
	require.NotEqual(t, amb.Candidates[0], amb.Candidates[1],
		"candidates must differ; two identical strings leave the user no way to disambiguate")

	// Every listed candidate must resolve on its own - the error is only useful if
	// what it prints can be pasted straight back in.
	got := map[string]string{}
	for _, cand := range amb.Candidates {
		data, _, cerr := s.ByRef(cand)
		require.NoError(t, cerr, "candidate %q from the error message must resolve", cand)
		got[cand] = string(data)
	}
	assert.ElementsMatch(t, []string{"output from A\n", "output from B\n"},
		[]string{got[amb.Candidates[0]], got[amb.Candidates[1]]},
		"the two candidates must reach the two different outputs")

	// The rendered message names the ref and both candidates, since that string is
	// the entire recovery affordance.
	msg := amb.Error()
	assert.Contains(t, msg, "ambiguous")
	assert.Contains(t, msg, amb.Candidates[0])
	assert.Contains(t, msg, amb.Candidates[1])
}

// TestDescriptorByRefSkipsTheBlob: the identity views want metadata only. It must
// agree with ByRef's descriptor, and - unlike ByRef, which still has bytes to hand
// back - it must ERROR when the descriptor is unreadable rather than quietly
// returning a zero-valued record that reads as a real run.
func TestDescriptorByRefSkipsTheBlob(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)
	const key = "1122334455667788990011223344556677889900112233445566"

	ref := mustPersist(t, s, key, []byte("a very large captured log\n"),
		OutputDescriptor{Project: "svc/api", Target: "test", TimestampMs: 42, DurationMs: 7})

	desc, err := s.DescriptorByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, "svc/api", desc.Project)
	assert.Equal(t, "test", desc.Target)
	assert.Equal(t, int64(42), desc.TimestampMs)

	_, viaBytes, err := s.ByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, viaBytes, desc, "both readers must report the same identity")

	// Remove the sidecar: the blob still resolves, so ByRef degrades to a zero
	// descriptor while DescriptorByRef must say it cannot answer.
	require.NoError(t, os.Remove(filepath.Join(dir, "outputs", key, desc.Attempt+descExt)))
	_, zero, err := s.ByRef(ref)
	require.NoError(t, err, "bytes are still retrievable")
	assert.Empty(t, zero.Project, "ByRef degrades to a zero descriptor")
	_, err = s.DescriptorByRef(ref)
	assert.Error(t, err, "a metadata reader must not invent an empty run")

	_, err = s.DescriptorByRef("outffffffffffff")
	assert.ErrorIs(t, err, fs.ErrNotExist, "an unknown ref is not-exist, not a zero record")
}

// TestRefNotFoundNamesTheStoresConsulted: "not found" is only actionable if the
// reader learns WHERE magus looked - a never-published foreign ref and a typo are
// otherwise the same message. It must also keep matching fs.ErrNotExist so the CLI's
// existing MGS8001 path still fires.
func TestRefNotFoundNamesTheStoresConsulted(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")

	_, _, err := c.OutputByRef(context.Background(), "outdeadbeefcafe")
	require.Error(t, err)
	assert.ErrorIs(t, err, fs.ErrNotExist, "the CLI's not-found diagnostic keys on this")

	var missing *RefNotFoundError
	require.True(t, errors.As(err, &missing), "a miss must name the stores, got %v", err)
	assert.Equal(t, []string{"local cache"}, missing.Stores,
		"with no remote configured, claiming a remote was consulted would be a lie")
	msg := missing.Error()
	assert.Contains(t, msg, "outdeadbeefcafe")
	assert.Contains(t, msg, "local cache")
	assert.True(t, strings.Contains(msg, "consulted"), "the message must say where magus looked: %q", msg)
}
