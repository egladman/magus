package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/observability/otlp"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
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
	srv, err := proc.New(proc.Options{
		Handler: func(context.Context, []string) error { return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())
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
	addr := "unix://" + proc.SockDir() + "/magus-absent-test.sock"
	err := serverStop(context.Background(), []string{"--socket", addr})
	require.Error(t, err, "stop against a dead socket must report failure")

	var silent errSilent
	require.ErrorAs(t, err, &silent)
	assert.NotZero(t, silent.exitCode, "stopping nothing must exit non-zero")
}

// TestServerStopStalePools drives --pools against a live per-process pool whose
// display version differs from this binary: the leftover magus mcp / orphaned-run case
// that plain server stop never sees (it only dials server.sock).
func TestServerStopStalePools(t *testing.T) {
	privateSockDir(t)
	addr := "unix://" + filepath.Join(proc.SockDir(), "magus-424242-deadbeef.sock")
	srv, err := proc.New(proc.Options{
		Address: addr,
		Version: "stale-build",
		Handler: func(context.Context, []string) error { return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())
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
	assert.Zero(t, *spawned, "a second broker was started over the live one")
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
	assert.Equal(t, 1, *spawned, "with nothing serving, a run starts the broker")
}

// trapBrokerSpawn replaces the spawn seam for one test and counts the calls. No process
// is ever started: a unit test that re-execs the binary leaves a detached broker behind
// on every failure path.
func trapBrokerSpawn(t *testing.T) *int {
	t.Helper()
	calls := 0
	old := spawnBroker
	spawnBroker = func() (int, string, error) {
		calls++
		return 0, "", errors.New("spawn trapped by the test")
	}
	t.Cleanup(func() { spawnBroker = old })
	return &calls
}

// TestAnnounceBrokerSaysWhatItLeftBehind pins the one line a run prints when it starts
// the broker: the pid, that it opens no network listener, and when it goes away; nothing
// under -q or -s, and a record rather than prose under a structured -o.
func TestAnnounceBrokerSaysWhatItLeftBehind(t *testing.T) {
	var text strings.Builder
	announceBroker(&text, 4242, "", false)
	assert.Contains(t, text.String(), "started a broker (pid 4242)")
	assert.Contains(t, text.String(), "no network listener")
	assert.Contains(t, text.String(), "10 minutes")

	var quiet strings.Builder
	announceBroker(&quiet, 4242, "", true)
	assert.Empty(t, quiet.String(), "-q and -s drop it")

	var none strings.Builder
	announceBroker(&none, 0, "", false)
	assert.Empty(t, none.String(), "a broker this run did not start is not announced")

	var record strings.Builder
	announceBroker(&record, 4242, "jsonl", false)
	assert.True(t, strings.HasPrefix(record.String(), "{"), "a structured -o gets a record: %s", record.String())
	assert.Contains(t, record.String(), `"pid":4242`)
}

// TestServerChildArgsDropsStart pins the detached server's argv: its role, with no
// `start` in ps, and every flag the person passed so it binds where the parent waits.
func TestServerChildArgsDropsStart(t *testing.T) {
	assert.Equal(t, []string{"server", "--foreground"}, serverChildArgs([]string{"server", "start"}))
	assert.Equal(t,
		[]string{"--server-address", "unix:///tmp/m.sock", "server", "--foreground", "-v"},
		serverChildArgs([]string{"--server-address", "unix:///tmp/m.sock", "server", "start", "-v"}))
}

func TestIsServerRun(t *testing.T) {
	assert.True(t, isServerRun([]string{"start"}))
	assert.True(t, isServerRun([]string{"start", "--foreground"}))
	assert.True(t, isServerRun([]string{"--foreground"}), "the detached child runs the server")
	assert.False(t, isServerRun([]string{"start", "-h"}), "help prints usage and builds nothing")
	assert.False(t, isServerRun([]string{"stop"}))
	assert.False(t, isServerRun(nil))
}

// TestDetachedChildEnvDropsInheritedInvocationState pins that A BACKGROUND PROCESS
// DESCENDS FROM NOBODY. A run is what starts the broker, so without the scrub its
// environment permanently records that one run's ancestry, and a server started from
// inside a run would read those refs as its own, excusing claims from an invocation that
// ended hours ago and judging an unstamped run to be a nested magus that lost its
// ancestry. The same rule submitJob already applies to a job's context.
func TestDetachedChildEnvDropsInheritedInvocationState(t *testing.T) {
	t.Setenv("MAGUS_PROC_SOCKET", "unix:///tmp/parent.sock")
	t.Setenv("MAGUS_PROC_TOKEN", "parent-secret")
	t.Setenv("MAGUS_INVOCATION_ANCESTORS", "3217:inv-parent")
	t.Setenv("MAGUS_LEVEL", "1")
	t.Setenv("MAGUS_KEEP_ME", "yes")

	got := map[string]string{}
	for _, kv := range detachedChildEnv() {
		if name, value, ok := strings.Cut(kv, "="); ok {
			got[name] = value
		}
	}
	assert.NotContains(t, got, "MAGUS_PROC_SOCKET", "a child inheriting it binds no socket of its own")
	assert.NotContains(t, got, "MAGUS_PROC_TOKEN", "the parent socket's secret leaves with its address")
	assert.NotContains(t, got, "MAGUS_INVOCATION_ANCESTORS", "a background process is nobody's descendant")
	assert.NotContains(t, got, "MAGUS_LEVEL", "nor is it nested inside the run that happened to start it")
	assert.Equal(t, "yes", got["MAGUS_KEEP_ME"], "everything else is inherited as before")
}

// TestDetachedCmdReExecsTheGivenPathWithoutResolvingIt pins the fix for the v0.4.1 windows
// release, where auto-starting a background process failed with
//
//	exec: "C:\hostedtoolcache\windows\magus\bin\magus": executable file not found in %PATH%
//
// exec.Command resolves an absolute path on Windows through lookExtensions, which needs a
// PATHEXT sibling; setup-magus installs an extensionless `magus`, so the running binary
// could not re-exec itself. Nothing here should consult PATH: the caller passes
// os.Executable().
//
// The assertion is contract-shaped rather than reproducing the failure, which needs a
// Windows runtime. exec.Command sets Err on exactly this input there and nowhere else, so
// the case is stated on every platform and enforced where it bites.
func TestDetachedCmdReExecsTheGivenPathWithoutResolvingIt(t *testing.T) {
	for _, tc := range []struct{ name, exe string }{
		{"extensionless, as setup-magus installs it", filepath.Join(t.TempDir(), "magus")},
		{"with the windows extension", filepath.Join(t.TempDir(), "magus.exe")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := detachedCmd(tc.exe, []string{"broker"})

			require.NoError(t, cmd.Err, "re-execing a known absolute path performs no lookup that could fail")
			assert.Equal(t, tc.exe, cmd.Path, "the path is used verbatim, never a PATHEXT sibling")
			assert.Equal(t, []string{tc.exe, "broker"}, cmd.Args,
				"argv[0] is the executable, with the caller's arguments after it")
		})
	}
}

// TestDetachedCmdIgnoresPATH proves the resolution is absent rather than merely succeeding:
// a name that also exists on PATH must not be picked up in place of the path given.
func TestDetachedCmdIgnoresPATH(t *testing.T) {
	decoy := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(decoy, "magus"), []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", decoy)

	want := filepath.Join(t.TempDir(), "magus")
	cmd := detachedCmd(want, nil)

	assert.Equal(t, want, cmd.Path, "the decoy on PATH is not consulted")
	assert.NotEqual(t, filepath.Join(decoy, "magus"), cmd.Path)
}

func TestIsServerStartHelpSkipsTheSubcommand(t *testing.T) {
	assert.True(t, isServerStartHelp([]string{"start", "-h"}))
	assert.True(t, isServerStartHelp([]string{"start", "--foreground", "--help"}))
	assert.True(t, isServerStartHelp([]string{"start", "help"}))
	assert.False(t, isServerStartHelp([]string{"start"}))
	assert.False(t, isServerStartHelp([]string{"start", "--foreground"}))
	assert.False(t, isServerStartHelp([]string{"help"}), "the subcommand itself is not a help flag")
}

func TestWantsForeground(t *testing.T) {
	assert.True(t, wantsForeground([]string{"start", "--foreground"}))
	assert.True(t, wantsForeground([]string{"start", "-foreground"}))
	// A pre-parse scanner takes only the bare spellings; an =value form is not a bool.
	assert.False(t, wantsForeground([]string{"start", "--foreground=true"}))
	assert.False(t, wantsForeground([]string{"start"}))
}

func TestConsoleURLsDegradeToEmpty(t *testing.T) {
	prev := globalCfg.Console.Enabled
	t.Cleanup(func() { globalCfg.Console.Enabled = prev })

	off := false
	globalCfg.Console.Enabled = &off
	assert.Equal(t, "", consoleWatchURL())
	assert.Equal(t, "", consoleDiffURL())
}

// The message exists because "already running" answered a question nobody asked. A second
// worktree's `server start` returns 0 having loaded nothing from that tree, and the console then
// shows the tree the server was started in, which reads as success.
func TestServingSuffixNamesTheLoadedWorkspaces(t *testing.T) {
	st := &proc.StatusReply{Workspaces: []proc.Workspace{
		{Root: "/repo/worktrees/b"},
		{Root: "/repo"},
	}}

	// Sorted, so two runs of the same server do not print the list two ways.
	assert.Equal(t, ", serving /repo, /repo/worktrees/b", servingSuffix(st))

	// A server that has loaded nothing yet says nothing rather than "serving " with an empty
	// list, which would read as a server that is serving something unnameable.
	assert.Empty(t, servingSuffix(&proc.StatusReply{}))
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

// checkReviewWorkspace opens a throwaway workspace and wires a review provider whose
// review_threads answer the caller chooses, so the job can be driven without a forge.
//
// The provider is asked for a MERGED review, since the merged report is the job's whole output
// and the branch under test decides whether it is written.
func checkReviewWorkspace(t *testing.T, threads func() (any, error), opts ...magus.Option) (context.Context, string, *magus.Magus) {
	t.Helper()
	return checkReviewWorkspaceIn(t, "merged", threads, opts...)
}

// checkReviewWorkspaceIn is checkReviewWorkspace for a review the host reports in state.
func checkReviewWorkspaceIn(t *testing.T, state string, threads func() (any, error), opts ...magus.Option) (context.Context, string, *magus.Magus) {
	t.Helper()
	// The job parses its own flags, and that binding writes defaults into the global config.
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	// The trail is read back per workspace, so a developer pointing every cache at one directory
	// would have these two tests reading each other's events.
	t.Setenv("MAGUS_CACHE_DIR", "")

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"),
		[]byte("import \"magus\";\n\nmagus\\project({})\n"), 0o644))
	m, err := magus.Open(context.Background(), root, opts...)
	require.NoError(t, err, "fixture workspace must open")

	name := "fake-job-review-" + t.Name()
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(name,
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			switch req.Target {
			case spells.FindReviewContract:
				return map[string]any{"id": "482", "repo": "acme/acme", "state": state}, nil
			case spells.ReviewThreadsContract:
				return threads()
			default:
				return map[string]any{}, nil
			}
		})))
	prev := bindings.ReviewProvider()
	bindings.SetReviewProvider(name)
	t.Cleanup(func() { bindings.SetReviewProvider(prev) })

	return withMagus(context.Background(), m), root, m
}

// mergedReviewEvents returns the review.merged events the job left on the trail.
func mergedReviewEvents(t *testing.T, cacheDir string) []trail.Event {
	t.Helper()
	events, err := trail.ReadRecent(cacheDir, 20)
	require.NoError(t, err)
	var out []trail.Event
	for _, e := range events {
		if e.Action == "review.merged" {
			out = append(out, e)
		}
	}
	return out
}

// One unreadable remark must not blank the merge report. The threads that decoded are in hand,
// and the job's error is a MALFORMED record rather than an unreachable host; reading it as the
// latter meant a single bad row suppressed the only notice this merge would ever get, and the
// conversation was then gone with the branch.
func TestCheckReviewReportsAMergeDespiteAMalformedRemark(t *testing.T) {
	ctx, root, m := checkReviewWorkspace(t, func() (any, error) {
		return []any{
			map[string]any{"id": "t1", "path": "a.go", "line": 11, "author": "priya", "body": "theirs"},
			map[string]any{"id": "t2", "line": "not a number"},
		}, nil
	})
	// The watermark is the opt-in: without it the job asks the forge nothing at all. Marking the
	// readable thread seen also keeps review.said out of the way of the assertion below.
	reader := changeset.NewStore(m.CacheDir())
	reader.Attach(root, "main", types.Diff{Base: "main"}, "asof")
	reader.MarkCommentsSeen(root, []string{"t1"})

	require.NoError(t, serverCheckReview(ctx, root, nil))

	merged := mergedReviewEvents(t, m.CacheDir())
	require.Len(t, merged, 1, "a malformed remark must not suppress the merge report")
	assert.Contains(t, merged[0].Preview, "acme/acme")
}

// A forge that could not be reached says nothing rather than reporting a count it derived from
// an empty list. The drafts here are real and local, so the pre-fix reading emitted "1 remark"
// about a review whose whole conversation was unread.
func TestCheckReviewSaysNothingWhenTheForgeCouldNotBeReached(t *testing.T) {
	ctx, root, m := checkReviewWorkspace(t, func() (any, error) {
		return nil, errors.New("dial: connection refused")
	})
	reader := changeset.NewStore(m.CacheDir())
	reader.Attach(root, "main", types.Diff{Base: "main"}, "asof")
	reader.AddComment(root, types.DiffComment{Path: "a.go", Line: 4, Body: "mine"}, types.DiffAuthorUnattributed)

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
	ctx, _, m := checkReviewWorkspace(t, func() (any, error) { return []any{}, nil }, magus.WithProvider(rec))
	started := time.Now().Add(-90 * time.Second)
	_, err := changeset.NewStore(m.CacheDir()).SetReading(ctx, types.ReviewTarget{ID: "482", Repo: "acme/acme"}, started)
	require.NoError(t, err)

	require.NoError(t, serverCheckReview(ctx, m.Root(), nil))

	merged := mergedReviewEvents(t, m.CacheDir())
	require.Len(t, merged, 1)
	assert.Equal(t, "acme/acme: 0", merged[0].Preview)
	require.Len(t, rec.secs, 1, "one merge under a reader is one observation")
	assert.InDelta(t, 90.0, rec.secs[0], 5.0, "the duration is the time from the mark to the merge")
	assert.False(t, changeset.NewStore(m.CacheDir()).LoadReading().Active(), "reported once: the mark is cleared")

	// The next tick finds nothing to report, and counts nothing.
	require.NoError(t, serverCheckReview(ctx, m.Root(), nil))
	assert.Len(t, mergedReviewEvents(t, m.CacheDir()), 1)
	assert.Len(t, rec.secs, 1)
}

// A mark set against another review says nothing about this merge. The branch has moved to a new
// pull request, and counting the old mark would put a reader under a merge they never started on.
func TestCheckReviewIgnoresAMarkForAnotherReview(t *testing.T) {
	rec := newMergeWhileReadingRecorder(t)
	ctx, _, m := checkReviewWorkspace(t, func() (any, error) { return []any{}, nil }, magus.WithProvider(rec))
	_, err := changeset.NewStore(m.CacheDir()).SetReading(ctx, types.ReviewTarget{ID: "7", Repo: "acme/acme"}, time.Now())
	require.NoError(t, err)

	require.NoError(t, serverCheckReview(ctx, m.Root(), nil))

	assert.Empty(t, mergedReviewEvents(t, m.CacheDir()), "nothing was said and the mark is not this review's")
	assert.Empty(t, rec.secs)
	assert.True(t, changeset.NewStore(m.CacheDir()).LoadReading().Active(), "another review's mark is not this job's to clear")
}

// The job reads the mark, then waits on the forge. A mark the reader set on another review in the
// meantime is theirs: the merge is still reported for the mark that was read, but the new mark
// survives and the metric, which belongs to whoever clears the mark, is not counted.
func TestCheckReviewKeepsAMarkSetWhileItWasAskingTheForge(t *testing.T) {
	rec := newMergeWhileReadingRecorder(t)
	var store *changeset.Store
	ctx, _, m := checkReviewWorkspace(t, func() (any, error) {
		_, err := store.SetReading(context.Background(), types.ReviewTarget{ID: "7", Repo: "acme/acme"}, time.Now())
		require.NoError(t, err)
		return []any{}, nil
	}, magus.WithProvider(rec))
	store = changeset.NewStore(m.CacheDir())
	_, err := store.SetReading(ctx, types.ReviewTarget{ID: "482", Repo: "acme/acme"}, time.Now().Add(-time.Minute))
	require.NoError(t, err)

	require.NoError(t, serverCheckReview(ctx, m.Root(), nil))

	assert.Len(t, mergedReviewEvents(t, m.CacheDir()), 1, "the merge under the old mark is reported")
	assert.Equal(t, "7", store.LoadReading().Review, "the mark set in between is not deleted")
	assert.Empty(t, rec.secs, "the mark was not this job's to clear, so it is not counted")
}

// A mark older than the TTL is a forgotten tab. It neither reports a merge nor keeps the job
// asking the forge, and it is cleared.
func TestCheckReviewIgnoresAndClearsAnExpiredMark(t *testing.T) {
	rec := newMergeWhileReadingRecorder(t)
	ctx, _, m := checkReviewWorkspace(t, func() (any, error) { return []any{}, nil }, magus.WithProvider(rec))
	store := changeset.NewStore(m.CacheDir())
	_, err := store.SetReading(ctx, types.ReviewTarget{ID: "482", Repo: "acme/acme"}, time.Now().Add(-changeset.ReadingTTL-time.Minute))
	require.NoError(t, err)

	require.NoError(t, serverCheckReview(ctx, m.Root(), nil))

	assert.Empty(t, mergedReviewEvents(t, m.CacheDir()), "an expired mark is not a reader")
	assert.Empty(t, rec.secs)
	assert.False(t, store.LoadReading().Active(), "the expired mark is cleared")
}

// A review closed without merging will never merge, so the mark that was waiting on it goes.
func TestCheckReviewClearsTheMarkOfAReviewClosedUnmerged(t *testing.T) {
	rec := newMergeWhileReadingRecorder(t)
	ctx, _, m := checkReviewWorkspaceIn(t, "closed", func() (any, error) { return []any{}, nil }, magus.WithProvider(rec))
	store := changeset.NewStore(m.CacheDir())
	_, err := store.SetReading(ctx, types.ReviewTarget{ID: "482", Repo: "acme/acme"}, time.Now())
	require.NoError(t, err)

	require.NoError(t, serverCheckReview(ctx, m.Root(), nil))

	assert.False(t, store.LoadReading().Active(), "nothing is left to wait for")
	assert.Empty(t, mergedReviewEvents(t, m.CacheDir()))
	assert.Empty(t, rec.secs)
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
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"),
		[]byte("import \"magus\";\n\nmagus\\project({})\n"), 0o644))
	m, err := magus.Open(context.Background(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	own := &shutdownRecorder{Provider: base}
	shutdownOneShotTelemetry(context.Background(), own)
	assert.Equal(t, 1, own.shutdowns, "a one-shot process flushes before it exits")

	shared := &shutdownRecorder{Provider: base}
	shutdownOneShotTelemetry(withMagus(context.Background(), m), shared)
	assert.Zero(t, shared.shutdowns, "the server's provider is not the job's to shut down")
}

// With a remark on the merged review the report is today's, and a reader who was not reading
// costs the metric nothing: the two facts are independent.
func TestCheckReviewCountsNoReadingWhenNobodyWasReading(t *testing.T) {
	rec := newMergeWhileReadingRecorder(t)
	ctx, root, m := checkReviewWorkspace(t, func() (any, error) {
		return []any{map[string]any{"id": "t1", "path": "a.go", "line": 11, "author": "priya", "body": "theirs"}}, nil
	}, magus.WithProvider(rec))
	reader := changeset.NewStore(m.CacheDir())
	reader.Attach(root, "main", types.Diff{Base: "main"}, "asof")
	reader.MarkCommentsSeen(root, []string{"t1"})

	require.NoError(t, serverCheckReview(ctx, root, nil))

	assert.Len(t, mergedReviewEvents(t, m.CacheDir()), 1)
	assert.Empty(t, rec.secs)
}

// TestDetachedChildEnvDropsTheInheritedSocket pins the scrub. A child that inherits
// MAGUS_PROC_SOCKET decides it is already adopted, binds no socket of its own, and then
// reports the parent's, leaving a server `server stop` cannot find.
func TestDetachedChildEnvDropsTheInheritedSocket(t *testing.T) {
	t.Setenv("MAGUS_PROC_SOCKET", "/tmp/magus-parent.sock")
	t.Setenv("MAGUS_KEEP_ME", "1")

	env := detachedChildEnv()

	for _, kv := range env {
		assert.False(t, strings.HasPrefix(kv, "MAGUS_PROC_SOCKET="), "child inherited %q", kv)
	}
	assert.Contains(t, env, "MAGUS_KEEP_ME=1", "the scrub must drop one variable, not the environment")
}
