package cache

import (
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
	"strings"
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
		Schema: descriptorSchema, Key: "deadbeefcafef00d", KeyVersion: KeyVersion,
		Attempt: desc.Attempt,
	}, desc)
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
		Schema: descriptorSchema, Key: "cafebabecafebabe", KeyVersion: KeyVersion,
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
	assert.Equal(t, 2, desc.Schema)
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
	assert.Zero(t, desc.Schema)

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
