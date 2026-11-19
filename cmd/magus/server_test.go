package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/egladman/magus/types/diff"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/observability/otlp"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/server"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerStopTerminatesLiveServer drives the real serverStop handler against a live
// in-process proc server, pinning the fix for the silent no-op stop: stop must resolve the
// server, send the shutdown, and verify the socket has actually gone quiet before returning
// success. A full `server start` server cannot be driven from a unit test (its auto-background
// path re-execs the binary), so this exercises the discovery+verify logic that stop owns.
func TestServerStopTerminatesLiveServer(t *testing.T) {
	// Let proc pick a random socket under SockDir; a t.TempDir() path can exceed the unix
	// socket path length limit on macOS.
	srv, err := proc.New(t.Context(), proc.Options{
		Limiter: cache.NewLimiter(4),
		Handler: func(context.Context, []string) error { return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start(t.Context()))
	addr := srv.Addr()
	require.True(t, proc.SocketLive(context.Background(), addr), "server should be live before stop")

	// The explicit --socket bypasses config/discovery so stop targets exactly this server.
	err = serverStop(context.Background(), []string{"--socket", addr})
	require.NoError(t, err, "stop against a live server must succeed")

	assert.False(t, proc.SocketLive(context.Background(), addr), "stop must actually terminate the server")
	select {
	case <-srv.Done():
	default:
		t.Fatal("stop returned without the server having been closed")
	}
}

// TestServerStopNoServerExitsNonzero pins the other half of the fix: stop against nothing must
// not exit 0 silently. It returns a non-zero exit (errSilent) rather than pretending success.
func TestServerStopNoServerExitsNonzero(t *testing.T) {
	addr := fmt.Sprintf("unix://%s/magus-absent-test.sock", proc.SockDir())
	err := serverStop(context.Background(), []string{"--socket", addr})
	require.Error(t, err, "stop against a dead socket must report failure")

	var silent reportedExit
	require.ErrorAs(t, err, &silent)
	assert.NotZero(t, silent.ExitCode(), "stopping nothing must exit non-zero")
}

// TestServerStopStalePools drives --pools against a live per-process pool whose
// display version differs from this binary: the leftover magus mcp / orphaned-run case
// that plain server stop never sees (it only dials server.sock).
func TestServerStopStalePools(t *testing.T) {
	privateSockDir(t)
	addr := "unix://" + filepath.Join(proc.SockDir(), "magus-424242-deadbeef.sock")
	srv, err := proc.New(t.Context(), proc.Options{
		Limiter: cache.NewLimiter(4),
		Address: addr,
		Version: "stale-build",
		Handler: func(context.Context, []string) error { return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start(t.Context()))
	require.True(t, proc.SocketLive(context.Background(), addr))

	err = serverStop(context.Background(), []string{"--pools"})
	require.NoError(t, err)
	assert.False(t, proc.SocketLive(context.Background(), addr), "stale pool parent must stop")
}

func TestServerStopStalePoolsNone(t *testing.T) {
	privateSockDir(t)
	err := serverStop(context.Background(), []string{"--pools"})
	require.NoError(t, err, "no stale parents is success, not an error")
}

// privateSockDir points the socket directory at a fresh one, so a test never adopts or
// stops the developer's own broker or server. Short: a t.TempDir() path can exceed the
// unix socket length limit on macOS.
func privateSockDir(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "mgbrk")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("MAGUS_PROC_SOCKET", "")
}

// TestEnsureBrokerAdoptsALiveOne pins the idempotent half of the auto-start: a run uses
// the broker already holding this host's capacity and never starts a second, which
// would lose the bind anyway.
//
// It asserts on the SPAWN, through the seam, because spawning is the whole observable
// effect. Checking that the incumbent is still alive passes just as well with the early
// return deleted, and leaks a real detached broker while doing it.
func TestEnsureBrokerAdoptsALiveOne(t *testing.T) {
	privateSockDir(t)
	spawned := trapBrokerSpawn(t)

	ln, err := broker.Listen(t.Context(), broker.DefaultAddr())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- broker.Serve(ctx, ln) }()
	t.Cleanup(func() { cancel(); <-served })

	assert.Zero(t, ensureBroker(t.Context()), "no broker was started, so none is announced")
	assert.Zero(t, spawned(), "a second broker was started over the live one")
}

// TestEnsureBrokerStartsOneWhenAbsent is the other half: with nothing serving, a run does
// start the broker. Together with the test above, the pair pins that the early return is
// a decision rather than an accident.
func TestEnsureBrokerStartsOneWhenAbsent(t *testing.T) {
	privateSockDir(t)
	spawned := trapBrokerSpawn(t)

	// The spawn is trapped, so nothing comes up. What is under test is that a start was
	// ATTEMPTED, and that a run whose broker never arrives is not blocked by it.
	assert.Zero(t, ensureBroker(t.Context()), "a broker that never came up is not announced")
	assert.Equal(t, 1, spawned(), "with nothing serving, a run starts the broker")
}

// trapBrokerSpawn routes the broker spawn to a file for one test and returns the count of
// starts it refused. No process is ever started: a unit test that re-execs the binary leaves
// a detached broker behind on every failure path.
func trapBrokerSpawn(t *testing.T) func() int {
	t.Helper()
	trap := filepath.Join(t.TempDir(), "spawns")
	t.Setenv(server.BrokerSpawnTrapEnv, trap)
	return func() int {
		got, err := os.ReadFile(trap)
		if errors.Is(err, os.ErrNotExist) {
			return 0
		}
		require.NoError(t, err)
		return strings.Count(string(got), "\n")
	}
}

// TestNoticeBrokerSaysWhatItLeftBehind pins the one line a run prints when it starts the
// broker: the pid, that it opens no network listener, and when it goes away.
func TestNoticeBrokerSaysWhatItLeftBehind(t *testing.T) {
	text := notices(t, func() { noticeBroker(t.Context(), 4242) })
	assert.True(t, strings.HasPrefix(text, "magus: started a broker (pid 4242)"), text)
	assert.Contains(t, text, "no network listener")
	assert.Contains(t, text, "10 minutes")

	none := notices(t, func() { noticeBroker(t.Context(), 0) })
	assert.Empty(t, none, "a broker this run did not start is not announced")
}

func TestConsoleURLsDegradeToEmpty(t *testing.T) {
	prev := globalCfg.Console.Enabled
	t.Cleanup(func() { globalCfg.Console.Enabled = prev })

	off := false
	globalCfg.Console.Enabled = &off
	assert.Equal(t, "", consoleWatchURL())
	assert.Equal(t, "", consoleDiffURL())
}

// TestEnsureConsoleServerReturnsWithoutSpawning pins the early return: a console that is
// already serving must not start a second server. Nothing else in the test suite reaches
// ensureConsoleServer, and the spawn path it guards is the one that leaves a process behind.
func TestEnsureConsoleServerReturnsWithoutSpawning(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // a bound bridge answers; 401 is reachable
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	globalCfg.MCP.Address = addr

	require.NoError(t, ensureConsoleServer(t.Context(), addr, t.TempDir()))
}

// checkReviewWorkspace opens a throwaway workspace and selects the review provider registered
// as provider, so the job can be driven without a forge. Most cases ask for a MERGED review,
// since the merged report is the job's whole output and the branch under test decides whether
// it is written.
func checkReviewWorkspace(t *testing.T, provider string, opts ...magus.Option) (context.Context, string, *magus.Magus) {
	t.Helper()
	// The job parses its own flags, and that binding writes defaults into the global config.
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	// The trail is read back per workspace, so a developer pointing every cache at one directory
	// would have these two tests reading each other's events.
	t.Setenv("MAGUS_CACHE_DIR", "")

	w := testkit.NewWorkspace(t)
	w.Magusfile("import \"magus\";\n\nmagus\\project({})\n")
	root := w.Root()
	m, err := magus.Open(context.Background(), root, append(opts, magus.WithSpawnRule(tier.Ceiling))...)
	require.NoError(t, err, "fixture workspace must open")

	prev := bindings.ReviewProvider()
	bindings.SetReviewProvider(provider)
	t.Cleanup(func() { bindings.SetReviewProvider(prev) })

	return withMagus(withSecrets(context.Background()), m), root, m
}

// reviewIn registers a provider whose review 482 on acme/acme is in state and holds threads.
func reviewIn(t *testing.T, state string, threads ...diff.ReviewComment) string {
	t.Helper()
	return register.ReviewSpell(t, diff.ReviewTarget{ID: "482", Repo: "acme/acme", State: state}, threads)
}

// mergedReviewAnswering registers a provider whose merged review answers review_threads with
// body, for the answers ReviewSpell cannot give: a malformed row, a throw, a call that waits.
func mergedReviewAnswering(t *testing.T, body string) string {
	t.Helper()
	return register.Spell(t, "fake-job-review", spells.WithFuncSource(fmt.Sprintf(`import "magus/spell";
import "http";
export fun find_review(target: Target, req: FindReviewRequest) > ReviewTarget {
    return ReviewTarget{ id = "482", repo = "acme/acme", state = "merged" };
}
export fun review_threads(target: Target, req: ReviewRef) > any !> any {
%s
}`, body)))
}

// mergedReviewEvents returns the review.merged events the job left on the trail.
func mergedReviewEvents(t *testing.T, cacheDir string) []journal.Call {
	t.Helper()
	events, err := loggedCalls(t.Context(), cacheDir, 20)
	require.NoError(t, err)
	var out []journal.Call
	for _, e := range events {
		if e.Name == "review.merged" {
			out = append(out, e)
		}
	}
	return out
}

// One unreadable remark must not blank the merge report. The job's error is a MALFORMED record
// rather than an unreachable host; reading it as the latter meant a single bad row suppressed
// the only notice this merge would ever get, and the conversation was then gone with the branch.
func TestCheckReviewReportsAMergeDespiteAMalformedRemark(t *testing.T) {
	ctx, root, m := checkReviewWorkspace(t, mergedReviewAnswering(t, `
    return [{"id": "t1", "path": "a.go", "line": 11, "author": "priya", "body": "theirs"}, {"id": "t2", "line": "not a number"}];`))
	// The watermark is the opt-in: without it the job asks the forge nothing at all. Marking the
	// readable thread seen also keeps review.said out of the way of the assertion below.
	reader := changeset.NewStore(m.CacheDir())
	reader.Attach(root, "main", diff.Diff{Base: "main"}, "asof")
	reader.MarkCommentsSeen(root, []string{"t1"})

	require.NoError(t, serverCheckReview(ctx, root, nil))

	merged := mergedReviewEvents(t, m.CacheDir())
	require.Len(t, merged, 1, "a malformed remark must not suppress the merge report")
	assert.Contains(t, merged[0].Preview, "acme/acme")
	assert.Contains(t, merged[0].Preview, "unknown", "the count is not guessed")
	assert.Contains(t, merged[0].Preview, "[1].line", "and the decode failure is named")
}

// A forge that could not be reached says nothing rather than reporting a count it derived from
// an empty list. The drafts here are real and local, so the pre-fix reading emitted "1 remark"
// about a review whose whole conversation was unread.
func TestCheckReviewSaysNothingWhenTheForgeCouldNotBeReached(t *testing.T) {
	ctx, root, m := checkReviewWorkspace(t, mergedReviewAnswering(t, `
    throw "dial: connection refused";`))
	reader := changeset.NewStore(m.CacheDir())
	reader.Attach(root, "main", diff.Diff{Base: "main"}, "asof")
	reader.AddComment(root, diff.Comment{Path: "a.go", Line: 4, Body: "mine"}, diff.AuthorUnattributed)

	require.NoError(t, serverCheckReview(ctx, root, nil))

	assert.Empty(t, mergedReviewEvents(t, m.CacheDir()),
		"a count taken from an unreachable host is a number nobody can act on")
}

// mergeWhileReadingRecorder is a real (disabled) provider that also remembers what the job told it
// about reviews that merged under a reader.
type mergeWhileReadingRecorder struct {
	observability.Provider
	secs []float64
}

func (r *mergeWhileReadingRecorder) RecordReviewMergedWhileReading(_ context.Context, secs float64) {
	r.secs = append(r.secs, secs)
}

func newMergeWhileReadingRecorder(t *testing.T) *mergeWhileReadingRecorder {
	t.Helper()
	base, err := otlp.New(t.Context(), observability.Config{})
	require.NoError(t, err)
	return &mergeWhileReadingRecorder{Provider: base}
}

// The reader was in the middle of it, so the merge is reported although nobody said anything: no
// remarks, no drafts, no seen threads. The mark is the only trace that a review was opened here.
func TestCheckReviewReportsAMergeUnderAReaderWithNothingSaid(t *testing.T) {
	rec := newMergeWhileReadingRecorder(t)
	ctx, _, m := checkReviewWorkspace(t, reviewIn(t, "merged"), workspace.WithTelemetryProvider(rec))
	started := time.Now().Add(-90 * time.Second)
	_, err := changeset.NewStore(m.CacheDir()).SetReading(ctx, diff.ReviewTarget{ID: "482", Repo: "acme/acme"}, started)
	require.NoError(t, err)

	require.NoError(t, serverCheckReview(ctx, m.Root(), nil))

	merged := mergedReviewEvents(t, m.CacheDir())
	require.Len(t, merged, 1)
	assert.Equal(t, "acme/acme: 0", merged[0].Preview)
	require.Len(t, rec.secs, 1, "one merge under a reader is one observation")
	assert.InDelta(t, 90.0, rec.secs[0], 5.0, "the duration is the time from the mark to the merge")
	assert.False(t, storedReading(t, changeset.NewStore(m.CacheDir())).Active(), "reported once: the mark is cleared")

	// The next tick finds nothing to report, and counts nothing.
	require.NoError(t, serverCheckReview(ctx, m.Root(), nil))
	assert.Len(t, mergedReviewEvents(t, m.CacheDir()), 1)
	assert.Len(t, rec.secs, 1)
}

// A mark set against another review says nothing about this merge. The branch has moved to a new
// pull request, and counting the old mark would put a reader under a merge they never started on.
func TestCheckReviewIgnoresAMarkForAnotherReview(t *testing.T) {
	rec := newMergeWhileReadingRecorder(t)
	ctx, _, m := checkReviewWorkspace(t, reviewIn(t, "merged"), workspace.WithTelemetryProvider(rec))
	_, err := changeset.NewStore(m.CacheDir()).SetReading(ctx, diff.ReviewTarget{ID: "7", Repo: "acme/acme"}, time.Now())
	require.NoError(t, err)

	require.NoError(t, serverCheckReview(ctx, m.Root(), nil))

	assert.Empty(t, mergedReviewEvents(t, m.CacheDir()), "nothing was said and the mark is not this review's")
	assert.Empty(t, rec.secs)
	assert.True(t, storedReading(t, changeset.NewStore(m.CacheDir())).Active(), "another review's mark is not this job's to clear")
}

// The job reads the mark, then waits on the forge. A mark the reader set on another review in the
// meantime is theirs: the merge is still reported for the mark that was read, but the new mark
// survives and the metric, which belongs to whoever clears the mark, is not counted.
func TestCheckReviewKeepsAMarkSetWhileItWasAskingTheForge(t *testing.T) {
	rec := newMergeWhileReadingRecorder(t)
	// The provider's review_threads calls this host before answering, which is where the
	// reader sets a mark on another review mid-job.
	var store *changeset.Store
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := store.SetReading(context.Background(), diff.ReviewTarget{ID: "7", Repo: "acme/acme"}, time.Now())
		assert.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(host.Close)
	ctx, _, m := checkReviewWorkspace(t, mergedReviewAnswering(t, fmt.Sprintf(`
    http\get("%s");
    return [<ReviewComment>];`, host.URL)), workspace.WithTelemetryProvider(rec))
	store = changeset.NewStore(m.CacheDir())
	_, err := store.SetReading(ctx, diff.ReviewTarget{ID: "482", Repo: "acme/acme"}, time.Now().Add(-time.Minute))
	require.NoError(t, err)

	require.NoError(t, serverCheckReview(ctx, m.Root(), nil))

	assert.Len(t, mergedReviewEvents(t, m.CacheDir()), 1, "the merge under the old mark is reported")
	assert.Equal(t, "7", storedReading(t, store).Review, "the mark set in between is not deleted")
	assert.Empty(t, rec.secs, "the mark was not this job's to clear, so it is not counted")
}

// A mark older than the TTL is a forgotten tab. It neither reports a merge nor keeps the job
// asking the forge, and it is cleared.
func TestCheckReviewIgnoresAndClearsAnExpiredMark(t *testing.T) {
	rec := newMergeWhileReadingRecorder(t)
	ctx, _, m := checkReviewWorkspace(t, reviewIn(t, "merged"), workspace.WithTelemetryProvider(rec))
	store := changeset.NewStore(m.CacheDir())
	_, err := store.SetReading(ctx, diff.ReviewTarget{ID: "482", Repo: "acme/acme"}, time.Now().Add(-changeset.ReadingTTL-time.Minute))
	require.NoError(t, err)

	require.NoError(t, serverCheckReview(ctx, m.Root(), nil))

	assert.Empty(t, mergedReviewEvents(t, m.CacheDir()), "an expired mark is not a reader")
	assert.Empty(t, rec.secs)
	assert.False(t, storedReading(t, store).Active(), "the expired mark is cleared")
}

// A review closed without merging will never merge, so the mark that was waiting on it goes.
func TestCheckReviewClearsTheMarkOfAReviewClosedUnmerged(t *testing.T) {
	rec := newMergeWhileReadingRecorder(t)
	ctx, _, m := checkReviewWorkspace(t, reviewIn(t, "closed"), workspace.WithTelemetryProvider(rec))
	store := changeset.NewStore(m.CacheDir())
	_, err := store.SetReading(ctx, diff.ReviewTarget{ID: "482", Repo: "acme/acme"}, time.Now())
	require.NoError(t, err)

	require.NoError(t, serverCheckReview(ctx, m.Root(), nil))

	assert.False(t, storedReading(t, store).Active(), "nothing is left to wait for")
	assert.Empty(t, mergedReviewEvents(t, m.CacheDir()))
	assert.Empty(t, rec.secs)
}

// storedReading is the store's mark, failing the test when it cannot be read.
func storedReading(t *testing.T, store *changeset.Store) changeset.ReadingMark {
	t.Helper()
	m, err := store.LoadReading()
	require.NoError(t, err)
	return m
}

// shutdownRecorder counts Shutdown calls on an otherwise real provider.
type shutdownRecorder struct {
	observability.Provider
	shutdowns int
}

func (r *shutdownRecorder) Shutdown(ctx context.Context) error {
	r.shutdowns++
	return r.Provider.Shutdown(ctx)
}

// A short-lived `magus server check-review` records the metric just before it exits, and the SDK
// exports on an interval, so the process flushes its own provider. A job the server runs shares
// the server's provider, which outlives the job.
func TestShutdownOneShotTelemetryFlushesOnlyAProviderThisProcessOwns(t *testing.T) {
	base, err := otlp.New(t.Context(), observability.Config{})
	require.NoError(t, err)
	w := testkit.NewWorkspace(t)
	w.Magusfile("import \"magus\";\n\nmagus\\project({})\n")
	m, err := magus.Open(context.Background(), w.Root(), magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	own := &shutdownRecorder{Provider: base}
	shutdownOneShotTelemetry(context.Background(), own)
	assert.Equal(t, 1, own.shutdowns, "a one-shot process flushes before it exits")

	shared := &shutdownRecorder{Provider: base}
	shutdownOneShotTelemetry(withMagus(withSecrets(context.Background()), m), shared)
	assert.Zero(t, shared.shutdowns, "the server's provider is not the job's to shut down")
}

// With a remark on the merged review the report is today's, and a reader who was not reading
// costs the metric nothing: the two facts are independent.
func TestCheckReviewCountsNoReadingWhenNobodyWasReading(t *testing.T) {
	rec := newMergeWhileReadingRecorder(t)
	ctx, root, m := checkReviewWorkspace(t,
		reviewIn(t, "merged", diff.ReviewComment{ID: "t1", Path: "a.go", Line: 11, Author: "priya", Body: "theirs"}),
		workspace.WithTelemetryProvider(rec))
	reader := changeset.NewStore(m.CacheDir())
	reader.Attach(root, "main", diff.Diff{Base: "main"}, "asof")
	reader.MarkCommentsSeen(root, []string{"t1"})

	require.NoError(t, serverCheckReview(ctx, root, nil))

	assert.Len(t, mergedReviewEvents(t, m.CacheDir()), 1)
	assert.Empty(t, rec.secs)
}

// TestServerAndBrokerSocketsSitApart pins the two addresses: each a unix socket in the
// private socket directory, named for what listens there, and never the same file.
func TestServerAndBrokerSocketsSitApart(t *testing.T) {
	server, brk := proc.ServerDefaultAddr(), broker.DefaultAddr()
	assert.True(t, strings.HasPrefix(server, "unix://"), server)
	assert.True(t, strings.HasSuffix(server, "/server.sock"), server)
	assert.True(t, strings.HasPrefix(brk, "unix://"), brk)
	assert.True(t, strings.HasSuffix(brk, "/"+broker.SocketName), brk)
	assert.Equal(t, filepath.Dir(server), filepath.Dir(brk))
}

// TestResolveServerAddrNeverConsultsTheAdoptionSocket pins that the server's address is
// config or server.sock, never MAGUS_PROC_SOCKET: inside a run that variable names the
// run's own per-process pool, which dies with it.
func TestResolveServerAddrNeverConsultsTheAdoptionSocket(t *testing.T) {
	defer snapshotGlobals()()
	t.Setenv(proc.SocketEnv, "unix:///tmp/magus-1-abc.sock")
	globalCfg.Server.Address = ""
	assert.Equal(t, proc.ServerDefaultAddr(), resolveServerAddr(""))
	globalCfg.Server.Address = "unix:///tmp/configured.sock"
	assert.Equal(t, "unix:///tmp/configured.sock", resolveServerAddr(""))
	assert.Equal(t, "unix:///tmp/flag.sock", resolveServerAddr("unix:///tmp/flag.sock"))
}

// useTextLogger makes the logger a command builds from its flags a text handler on
// whatever os.Stderr is then, so captureStderr sees its records. The pretty handler is
// one per terminal and stays on the real stderr, past the swap.
func useTextLogger(t *testing.T) {
	t.Helper()
	prevLog, prevFormat, prevLevel := slog.Default(), globalCfg.Log.Format, globalCfg.Log.Level
	prevQuiet, prevSilent := global.quiet, global.silent
	globalCfg.Log.Format, globalCfg.Log.Level = "text", "info"
	global.quiet, global.silent = false, false
	t.Cleanup(func() {
		slog.SetDefault(prevLog)
		globalCfg.Log.Format, globalCfg.Log.Level = prevFormat, prevLevel
		global.quiet, global.silent = prevQuiet, prevSilent
	})
}

// notices returns what the pretty display prints for the records fn logs through
// the default logger, the way a notice reaches a person now that it is a record.
func notices(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(resolverHandler{cache.NewLogHandler(&buf, "pretty", &slog.HandlerOptions{Level: slog.LevelDebug})}))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}
