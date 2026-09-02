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
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerStopTerminatesLiveDaemon drives the real serverStop handler against a live
// in-process proc server, pinning the fix for the silent no-op stop: stop must resolve the
// server, send the shutdown, and verify the socket has actually gone quiet before returning
// success. A full `server start` daemon cannot be driven from a unit test (its auto-background
// path re-execs the binary), so this exercises the discovery+verify logic that stop owns.
func TestServerStopTerminatesLiveDaemon(t *testing.T) {
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
	require.NoError(t, err, "stop against a live daemon must succeed")

	assert.False(t, proc.SocketLive(context.Background(), addr), "stop must actually terminate the daemon")
	select {
	case <-srv.Done():
	default:
		t.Fatal("stop returned without the server having been closed")
	}
}

// TestServerStopNoDaemonExitsNonzero pins the other half of the fix: stop against nothing must
// not exit 0 silently. It returns a non-zero exit (errSilent) rather than pretending success.
func TestServerStopNoDaemonExitsNonzero(t *testing.T) {
	addr := "unix://" + proc.SockDir() + "/magus-absent-test.sock"
	err := serverStop(context.Background(), []string{"--socket", addr})
	require.Error(t, err, "stop against a dead socket must report failure")

	var silent errSilent
	require.ErrorAs(t, err, &silent)
	assert.NotZero(t, silent.exitCode, "stopping nothing must exit non-zero")
}

// TestEnsureAdmissionDaemonAdoptsALiveOne pins the idempotent half of the auto-start: a
// run must adopt the daemon that is already arbitrating this machine, never spawn a
// second one. Two daemons would be two budgets, which is the exact failure the feature
// exists to remove, and `magus doctor` reports the pair as a fault.
//
// It asserts on the SPAWN, through the seam, because spawning is the whole observable
// effect. Checking that the incumbent is still alive passes just as well with the early
// return deleted - and leaks a real detached daemon while doing it.
func TestEnsureAdmissionDaemonAdoptsALiveOne(t *testing.T) {
	// A private socket dir, so this never adopts the developer's own daemon. Short: a
	// t.TempDir() path can exceed the unix socket length limit on macOS.
	dir, err := os.MkdirTemp("", "mgadmit")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(dir) }()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("MAGUS_DAEMON_SOCKET", "")
	spawned := trapAdmissionSpawn(t)

	addr := daemonDefaultAddr()
	srv, err := proc.New(proc.Options{
		Handler: func(context.Context, []string) error { return nil },
		Address: addr,
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	got := ensureAdmissionDaemon(context.Background(), addr)
	assert.Equal(t, addr, got, "the run arbitrates against the daemon that is already up")
	assert.Zero(t, *spawned, "a second daemon was started over the live one")
}

// TestEnsureAdmissionDaemonStartsOneWhenAbsent is the other half: with nothing serving,
// a run does start the arbiter. Together with the test above, the pair pins that the
// early return is a decision rather than an accident.
func TestEnsureAdmissionDaemonStartsOneWhenAbsent(t *testing.T) {
	dir, err := os.MkdirTemp("", "mgadmit")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(dir) }()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("MAGUS_DAEMON_SOCKET", "")
	spawned := trapAdmissionSpawn(t)

	// The spawn is trapped, so nothing comes up and the readiness wait fails. What is
	// under test is that a start was ATTEMPTED, and that a run whose daemon never
	// arrives is told there is no arbiter rather than being blocked.
	got := ensureAdmissionDaemon(context.Background(), daemonDefaultAddr())
	assert.Equal(t, 1, *spawned, "with nothing serving, a run starts the arbiter")
	assert.Empty(t, got, "and a daemon that never came up is reported as no arbiter, not as one")
}

// trapAdmissionSpawn replaces the spawn seam for one test and counts the calls. No
// process is ever started: a unit test that re-execs the binary leaves a detached
// daemon behind on every failure path.
func trapAdmissionSpawn(t *testing.T) *int {
	t.Helper()
	calls := 0
	old := spawnAdmissionDaemon
	spawnAdmissionDaemon = func() (int, string, error) {
		calls++
		return 0, "", errors.New("spawn trapped by the test")
	}
	t.Cleanup(func() { spawnAdmissionDaemon = old })
	return &calls
}

// TestAdmissionIdleExitIsOnlyForAnUnaskedDaemon pins the bound the doctrine amendment
// promises, and its limit: a daemon a person started stays up until they stop it.
func TestAdmissionIdleExitIsOnlyForAnUnaskedDaemon(t *testing.T) {
	srv, err := proc.New(proc.Options{Handler: func(context.Context, []string) error { return nil }})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	t.Setenv(admissionDaemonEnv, "")
	watchAdmissionIdle(t.Context(), srv)
	select {
	case <-srv.Done():
		t.Fatal("a daemon somebody started deliberately must not time itself out")
	case <-time.After(50 * time.Millisecond):
	}
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
// shows the tree the daemon was started in - which reads as success.
func TestServingSuffixNamesTheLoadedWorkspaces(t *testing.T) {
	st := &proc.StatusReply{Workspaces: []proc.Workspace{
		{Root: "/repo/worktrees/b"},
		{Root: "/repo"},
	}}

	// Sorted, so two runs of the same daemon do not print the list two ways.
	assert.Equal(t, ", serving /repo, /repo/worktrees/b", servingSuffix(st))

	// A daemon that has loaded nothing yet says nothing rather than "serving " with an empty
	// list, which would read as a daemon that is serving something unnameable.
	assert.Empty(t, servingSuffix(&proc.StatusReply{}))
}

// TestEnsureConsoleDaemonReturnsWithoutSpawning pins the early return: a console that is
// already serving must not start a second daemon. Nothing else in the test suite reaches
// ensureConsoleDaemon, and the spawn path it guards is the one that leaves a process behind.
func TestEnsureConsoleDaemonReturnsWithoutSpawning(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // a bound bridge answers; 401 is reachable
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	globalCfg.MCP.Address = addr

	require.NoError(t, ensureConsoleDaemon(t.Context(), addr, t.TempDir()))
}

// checkReviewWorkspace opens a throwaway workspace and wires a review provider whose
// review_threads answer the caller chooses, so the job can be driven without a forge.
//
// The provider is asked for a MERGED review, since the merged report is the job's whole output
// and the branch under test decides whether it is written.
func checkReviewWorkspace(t *testing.T, threads func() (any, error)) (context.Context, string, *magus.Magus) {
	t.Helper()
	// The job parses its own flags, and that binding writes defaults into the global config.
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	// The trail is read back per workspace, so a developer pointing every cache at one directory
	// would have these two tests reading each other's events.
	t.Setenv("MAGUS_CACHE_DIR", "")

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"),
		[]byte("import \"magus\";\n\nmagus.project({})\n"), 0o644))
	m, err := magus.Open(context.Background(), root)
	require.NoError(t, err, "fixture workspace must open")

	name := "fake-job-review-" + t.Name()
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(name,
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			switch req.Target {
			case spells.FindReviewContract:
				return map[string]any{"id": "482", "repo": "acme/acme", "state": "merged"}, nil
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
// and the job's error is a MALFORMED record rather than an unreachable host - reading it as the
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
	reader.MarkThreadsSeen(root, []string{"t1"})

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
	reader.AddComment(root, types.DiffComment{Path: "a.go", Line: 4, Body: "mine"}, types.DiffAuthorHuman)

	require.NoError(t, serverCheckReview(ctx, root, nil))

	assert.Empty(t, mergedReviewEvents(t, m.CacheDir()),
		"a count taken from an unreachable host is a number nobody can act on")
}

// TestDaemonChildEnvDropsTheInheritedSocket pins the scrub. A child that inherits
// MAGUS_DAEMON_SOCKET decides it is already adopted, binds no socket of its own, and then
// reports the parent's - leaving a daemon `server stop` cannot find.
func TestDaemonChildEnvDropsTheInheritedSocket(t *testing.T) {
	t.Setenv("MAGUS_DAEMON_SOCKET", "/tmp/magus-parent.sock")
	t.Setenv("MAGUS_KEEP_ME", "1")

	env := daemonChildEnv()

	for _, kv := range env {
		assert.False(t, strings.HasPrefix(kv, "MAGUS_DAEMON_SOCKET="), "child inherited %q", kv)
	}
	assert.Contains(t, env, "MAGUS_KEEP_ME=1", "the scrub must drop one variable, not the environment")
}
