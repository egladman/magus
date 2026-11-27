package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	qtypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// The records-swept log is read by its last line and compacted to one line once it is due.
func TestRecordsSweptKeepsItsLastPass(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	path, err := s.Path()
	require.NoError(t, err)
	rows := []job.Job{{ID: "a"}}
	passes := int64(2 + codec.CompactSlack + 1)
	for at := range passes {
		s.writeRecordsSwept(t.Context(), at, rows)
	}
	read, err := codec.ReadLines(filepath.Join(filepath.Dir(path), recordsSweptFile))
	require.NoError(t, err)
	assert.Len(t, read.Lines, 1, "the pass that made the log due rewrote it")
	assert.True(t, s.recordsSwept(t.Context(), passes-1, rows))
	assert.False(t, s.recordsSwept(t.Context(), passes-1, nil), "a row the last pass saw has gone")
}

// TestMain isolates the environment: lease markers live under XDG_STATE_HOME, and the
// parallel tests here cannot pin it one test at a time.
func TestMain(m *testing.M) { testkit.Main(m) }

// envStateBase is the user state dir the test's environment names, resolved as a door
// outside this package resolves it.
func envStateBase(t *testing.T) string {
	t.Helper()
	base, err := xdg.StateDir()
	require.NoError(t, err)
	return base
}

// placeAt is the Location of the checkout whose cache dir is cacheDir, under envStateBase.
func placeAt(t *testing.T, cacheDir string) Location {
	t.Helper()
	return Location{CacheDir: cacheDir, StateBase: envStateBase(t)}
}

// writeMarker writes content as cacheDir's checkout record.
func writeMarker(t *testing.T, cacheDir, content string) {
	t.Helper()
	path := placeAt(t, cacheDir).MarkerPath()
	require.NotEmpty(t, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// loadableRoot writes the smallest tree WorkspaceLoadFiles finds something in, and
// returns it.
func loadableRoot(t *testing.T) string {
	t.Helper()
	w := testkit.NewWorkspace(t)
	w.WriteTree(loadableFiles)
	return w.Root()
}

// loadableRepository is loadableRoot committed to a git repository, for a fixture that execs:
// Exec reads its base from the checkout.
func loadableRepository(t *testing.T) string {
	t.Helper()
	return testkit.NewRepository(t, loadableFiles).Root()
}

var loadableFiles = map[string]string{
	"magus.yaml":            "jobs:\n  stale_after: 2h\n",
	"magusfile.buzz":        "export fun build() {}\n",
	"internal/job/store.go": "package job\n",
}

// forkCheck is the check every writing fixture here declares, since a fork refuses a
// writing job with nothing to grade it by.
func forkCheck() job.Check {
	return fixtureCheck(types.LeaseCheck{Target: "test", Project: "."})
}

// TestForkRefusesAWorkspaceLoadWritePathInASharedCheckout is the refusal three workers in
// one checkout were missing: one of them held the root magusfile in its write paths, saved
// it mid-edit, and every `magus run` in the checkout failed until it landed, including the
// other two workers' tests.
//
// The refusal is narrow on purpose. Two workers' write paths overlapping is the
// orchestrator's problem and stays an advisory; a write path covering a file the WORKSPACE
// has to load is the one case where a worker doing exactly what it was told breaks work it
// cannot see.
func TestForkRefusesAWorkspaceLoadWritePathInASharedCheckout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := loadableRepository(t)
	loc := tempLocation(t, root)
	s := NewStore(loc)
	limits := config.Jobs{}

	held, err := ForkMerge(ctx, s, "wave/worker-one", func(u *job.Job) {
		u.Check, u.State, u.Paths.Write = forkCheck(), job.StateRunning, []string{"internal/job/store.go"}
	}, limits, nil)
	require.NoError(t, err)
	_, err = s.Exec(ctx, held.ID)
	require.NoError(t, err)

	_, err = ForkMerge(ctx, s, "wave/worker-two", func(u *job.Job) {
		u.Check, u.State, u.Paths.Write = forkCheck(), job.StateDeclared, []string{"magusfile.buzz", "docs"}
	}, limits, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "magusfile.buzz")
	assert.Contains(t, err.Error(), "wave/worker-one")
	assert.Contains(t, err.Error(), "worktree")

	t.Run("a write path that touches no workspace-load file still forks", func(t *testing.T) {
		_, err := ForkMerge(ctx, s, "wave/worker-three", func(u *job.Job) {
			u.Check, u.State, u.Paths.Write = forkCheck(), job.StateDeclared, []string{"internal/guard"}
		}, limits, nil)
		require.NoError(t, err)
	})

	t.Run("an empty checkout refuses nothing", func(t *testing.T) {
		alone := NewStore(tempLocation(t, root))
		_, err := ForkMerge(ctx, alone, "solo", func(u *job.Job) {
			u.Check, u.State, u.Paths.Write = forkCheck(), job.StateDeclared, []string{"magusfile.buzz"}
		}, limits, nil)
		require.NoError(t, err)
	})
}

// TestForkIntoItsOwnCheckoutJudgesThatCheckout is why a `--checkout` fork may claim a load
// file beside a live job: the new job edits its own checkout, where nobody holds anything.
func TestForkIntoItsOwnCheckoutJudgesThatCheckout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := loadableRepository(t)
	s := NewStore(tempLocation(t, root))
	limits := config.Jobs{}

	held, err := ForkMerge(ctx, s, "wave/worker-one", func(u *job.Job) {
		u.Check, u.State, u.Paths.Write = forkCheck(), job.StateRunning, []string{"internal/job/store.go"}
	}, limits, nil)
	require.NoError(t, err)
	_, err = s.Exec(ctx, held.ID)
	require.NoError(t, err)

	own := filepath.Join(t.TempDir(), "worker-two")
	loadClaim := func(u *job.Job) {
		u.Check, u.State, u.Paths.Write = forkCheck(), job.StateDeclared, []string{"magusfile.buzz"}
	}

	t.Run("a fork naming a checkout passes", func(t *testing.T) {
		stored, err := ForkMerge(ctx, s, "wave/worker-two", func(u *job.Job) {
			loadClaim(u)
			u.Worktree = &job.Worktree{Root: own}
		}, limits, nil)
		require.NoError(t, err)
		assert.Equal(t, job.WriteProofAlone, stored.WriteProof, "nobody else works in %s", own)
	})

	t.Run("the same fork without one is still refused", func(t *testing.T) {
		_, err := ForkMerge(ctx, s, "wave/worker-three", loadClaim, limits, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "wave/worker-one")
	})
}

// TestForkRecordsWhetherTheWritePathsWereProvenDisjoint pins the third half of the collision
// proof: the orchestrator is ADVISED to prove the write paths disjoint, and the row records
// whether they are, so a plan read later says which forks were proven and which were not.
func TestForkRecordsWhetherTheWritePathsWereProvenDisjoint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := loadableRepository(t)
	loc := tempLocation(t, root)
	s := NewStore(loc)

	alone, err := ForkMerge(ctx, s, "wave/first", func(u *job.Job) {
		u.Check, u.State, u.Paths.Write = forkCheck(), job.StateRunning, []string{"internal/job/store.go"}
	}, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, job.WriteProofAlone, alone.WriteProof, "nothing else holds the checkout")
	_, err = s.Exec(ctx, alone.ID)
	require.NoError(t, err)

	disjoint, err := ForkMerge(ctx, s, "wave/second", func(u *job.Job) {
		u.Check, u.State, u.Paths.Write = forkCheck(), job.StateDeclared, []string{"internal/guard"}
	}, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, job.WriteProofDisjoint, disjoint.WriteProof)

	overlapping, err := ForkMerge(ctx, s, "wave/third", func(u *job.Job) {
		u.Check, u.State, u.Paths.Write = forkCheck(), job.StateDeclared, []string{"internal/job/store.go"}
	}, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, job.WriteProofOverlapping, overlapping.WriteProof,
		"an overlap is recorded, never refused: widening a write path is the orchestrator's call")
}

// A job is bound to a checkout only by exec, so two jobs forked back to back are both
// unbound. The second still has a peer, and a glob against a file inside it is the pair
// the proof once called alone.
func TestForkProvesAgainstDeclaredJobsNoOneHasExecuted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := loadableRoot(t)
	s := NewStore(tempLocation(t, root))
	fork := func(name string, paths ...string) job.Job {
		t.Helper()
		row, err := ForkMerge(ctx, s, name, func(u *job.Job) {
			u.Check, u.State, u.Paths.Write = forkCheck(), job.StateDeclared, paths
		}, config.Jobs{}, nil)
		require.NoError(t, err)
		return row
	}

	assert.Equal(t, job.WriteProofAlone, fork("wave/glob", "internal/guard/*.go").WriteProof)
	assert.Equal(t, job.WriteProofOverlapping, fork("wave/file", "internal/guard/guard.go").WriteProof,
		"a file the earlier glob matches")
	assert.Equal(t, job.WriteProofDisjoint, fork("wave/other", "internal/guard/README.md", "docs/x.md").WriteProof,
		"unbound rows count, and a file the glob does not match is disjoint from it")
}

// TestForkRefusesADirectoryWritePath pins MGS3018 on both doors that fork by merge: a
// directory claims every file under it, so no existing one is declarable, project roots
// included. Only a directory the job creates passes.
func TestForkRefusesADirectoryWritePath(t *testing.T) {
	t.Parallel()

	w := testkit.NewWorkspace(t)
	w.WriteTree(loadableFiles)
	root := w.Root()
	w.Write("libs/lib/magusfile.buzz", "export fun build() {}\n")
	require.NoError(t, os.MkdirAll(w.Path("libs", "lib", "src"), 0o755))
	w.Write("node_modules/pkg/magusfile.buzz", "")

	cases := []struct {
		name  string
		paths []string
		want  string // "" forks; otherwise a substring of the refusal
	}{
		{"a file", []string{"internal/job/store.go"}, ""},
		{"a directory the job creates", []string{"internal/fresh"}, ""},
		{"a file pattern", []string{"internal/job/*.go", "internal/**/*_test.go"}, ""},
		{"a plain directory", []string{"internal/job"}, `"internal/job", inside project "."`},
		{"a directory inside a project", []string{"libs/lib/src"}, `"libs/lib/src", inside project "libs/lib"`},
		{"a directory spelled as a glob", []string{"internal/**"}, `"internal/**" (matching the directory "internal")`},
		{"a directory pattern", []string{"libs/*/src/*"}, `(matching the directory "libs/lib/src")`},
		{"a vendored tree holding a magusfile", []string{"node_modules/pkg"}, `"node_modules/pkg"`},
		{"a project root", []string{"libs/lib"}, `"libs/lib", the root of project "libs/lib"`},
		{"the workspace root, itself a project", []string{"."}, `".", the workspace root`},
		{"the workspace root spelled as a glob", []string{"**"}, `"**" (matching the directory "."), the workspace root`},
		{"a wildcard subtree of a project root", []string{"libs/lib/**"}, `"libs/lib/**" (matching the directory "libs/lib"), the root of project "libs/lib"`},
		{"every bad path at once", []string{"internal", "libs/lib/src"}, `"libs/lib/src", inside project "libs/lib" is a directory`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewStore(tempLocation(t, root))
			_, err := ForkMerge(context.Background(), s, "wave/job", func(u *job.Job) {
				u.Check, u.State, u.Paths.Write = forkCheck(), job.StateDeclared, tc.paths
			}, config.Jobs{}, nil)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.WritePathIsDirectory)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), "a write path names files")
			assert.Contains(t, err.Error(), "list the files the job will edit")
			rows, lerr := s.List(t.Context())
			require.NoError(t, lerr)
			assert.Empty(t, rows, "a refused fork writes no row")
		})
	}

	t.Run("an update to a row that exists is not a fork", func(t *testing.T) {
		t.Parallel()
		s := NewStore(tempLocation(t, root))
		ctx := context.Background()
		_, err := ForkMerge(ctx, s, "wave/job", func(u *job.Job) {
			u.Check, u.State, u.Paths.Write = forkCheck(), job.StateDeclared, []string{"internal/job/store.go"}
		}, config.Jobs{}, nil)
		require.NoError(t, err)
		_, err = ForkMerge(ctx, s, "wave/job", func(u *job.Job) { u.Agent = modelRequest("opus") }, config.Jobs{}, nil)
		require.NoError(t, err)
	})
}

// TestSharedCheckoutRefusalIgnoresAJobThatIsOver is why the rule reads the STATE: a
// checkout a job that passed was taken in is a checkout nobody is working in, and
// refusing the next fork over it would strand the worktree. An exited job's holder
// returned, so its editing is over too, though the row waits on its grade.
func TestSharedCheckoutRefusalIgnoresAJobThatIsOver(t *testing.T) {
	t.Parallel()

	for _, over := range []job.State{job.StatePass, job.StateExited} {
		t.Run(string(over), func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			root := loadableRepository(t)
			s := NewStore(tempLocation(t, root))

			done, err := ForkMerge(ctx, s, "wave/done", func(u *job.Job) {
				u.Check, u.State, u.Paths.Write = forkCheck(), job.StateRunning, []string{"magus.yaml"}
			}, config.Jobs{}, nil)
			require.NoError(t, err)
			_, err = s.Exec(ctx, done.ID)
			require.NoError(t, err)
			_, err = s.Update(ctx, done.ID, func(u *job.Job) { u.State = over })
			require.NoError(t, err)

			next, err := ForkMerge(ctx, s, "wave/next", func(u *job.Job) {
				u.Check, u.State, u.Paths.Write = forkCheck(), job.StateDeclared, []string{"magus.yaml"}
			}, config.Jobs{}, nil)
			require.NoError(t, err)
			assert.Equal(t, job.WriteProofAlone, next.WriteProof)
		})
	}
}

// The refusal names the live holder, how long since it wrote, the write paths it has not
// used and the narrowing that frees them, and asks the holder to release them.
func TestSharedCheckoutRefusalNamesTheHoldersUnusedPaths(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := loadableRepository(t)
	s := NewStore(tempLocation(t, root))
	held, err := ForkMerge(ctx, s, "wave/worker-one", func(u *job.Job) {
		u.Check, u.State, u.Paths.Write = forkCheck(), job.StateRunning, []string{"internal/job/store.go"}
	}, config.Jobs{}, nil)
	require.NoError(t, err)
	_, err = s.Exec(ctx, held.ID)
	require.NoError(t, err)
	rows, err := s.List(t.Context())
	require.NoError(t, err)

	ps := sharedCheckoutProblems(s, rows, job.Job{ID: "wave/worker-two", Paths: job.Paths{Write: []string{"magusfile.buzz"}}})
	require.Len(t, ps, 1)
	require.ErrorIs(t, ps.Error(), types.CheckoutConfigHeld)
	assert.Contains(t, ps[0].Message, "wave/worker-one is live in this checkout (")
	assert.Contains(t, ps[0].Message, "; running, updated")
	assert.Contains(t, ps[0].Message, "wave/worker-one has changed nothing under internal/job/store.go")
	assert.Contains(t, ps[0].Hint, "--depends-on wave/worker-one")
	assert.Contains(t, ps[0].Hint, "the root narrows wave/worker-one to what it has changed with `magus job apply wave/worker-one --release-write-path internal/job/store.go`, which keeps its postconditions")

	requests := releaseRequests(t, root)
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Contains(t, req.Message, "job wave/worker-one holds magusfile.buzz")
	assert.Equal(t, sessions.AttentionRequest{
		ID: req.ID, Invocation: req.Invocation, OpenedMs: req.OpenedMs,
		Outcome: "waiting", Severity: "warning", Source: "magus/job-release",
		Where: "magusfile.buzz", Files: []types.FileRef{{Value: "magusfile.buzz"}}, Message: req.Message,
	}, req)

	t.Run("a dry run files no request", func(t *testing.T) {
		t.Parallel()
		root := loadableRepository(t)
		s := NewStore(tempLocation(t, root))
		held, err := ForkMerge(ctx, s, "wave/worker-one", func(u *job.Job) {
			u.Check, u.State, u.Paths.Write = forkCheck(), job.StateRunning, []string{"internal/job/store.go"}
		}, config.Jobs{}, nil)
		require.NoError(t, err)
		_, err = s.Exec(ctx, held.ID)
		require.NoError(t, err)
		rows, err := s.List(t.Context())
		require.NoError(t, err)
		require.NotEmpty(t, sharedCheckoutProblems(s.dry(), rows, job.Job{ID: "wave/worker-two", Paths: job.Paths{Write: []string{"magusfile.buzz"}}}))
		assert.Empty(t, releaseRequests(t, root))
	})
}

// TestLeaseQueryResolvesInOneOrder pins the order every caller grades by. The claim ranks
// last: it is the one answer a worker can rewrite from its own shell, so a claim that
// outranked a record would let a worker bound to one job act under another's write paths.
func TestLeaseQueryResolvesInOneOrder(t *testing.T) {
	t.Parallel()

	type answer struct {
		Lease string
		From  job.LeaseSource
	}
	for name, tc := range map[string]struct {
		query LeaseQuery
		want  answer
	}{
		"nothing answers": {LeaseQuery{}, answer{}},
		"the claim alone": {
			LeaseQuery{Claim: "wave/claim"},
			answer{"wave/claim", job.LeaseSourceEnv},
		},
		"the checkout's record alone": {
			LeaseQuery{CheckoutJob: "wave/marker"},
			answer{"wave/marker", job.LeaseSourceMarker},
		},
		"a claim that agrees with the checkout's record": {
			LeaseQuery{CheckoutJob: "wave/marker", Claim: "wave/marker"},
			answer{"wave/marker", job.LeaseSourceMarker},
		},
		"the checkout's record over a different claim": {
			LeaseQuery{CheckoutJob: "wave/marker", Claim: "wave/claim"},
			answer{"wave/marker", job.LeaseSourceContested},
		},
		"the caller's record alone": {
			LeaseQuery{CallerJob: "wave/agent"},
			answer{"wave/agent", job.LeaseSourceAgent},
		},
		"the caller's record agreeing with the claim": {
			LeaseQuery{CallerJob: "wave/agent", Claim: "wave/agent"},
			answer{"wave/agent", job.LeaseSourceAgent},
		},
		"the caller's record over a different claim": {
			LeaseQuery{CallerJob: "wave/agent", Claim: "wave/claim"},
			answer{"wave/agent", job.LeaseSourceContested},
		},
		"the flag alone": {
			LeaseQuery{Flag: "wave/flag"},
			answer{"wave/flag", job.LeaseSourceFlag},
		},
		"the flag over a different claim": {
			LeaseQuery{Flag: "wave/flag", Claim: "wave/claim"},
			answer{"wave/flag", job.LeaseSourceContested},
		},
		"the flag over everything": {
			LeaseQuery{Flag: "wave/flag", CallerJob: "wave/agent", Claim: "wave/claim"},
			answer{"wave/flag", job.LeaseSourceContested},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lease, from := tc.query.Resolve()
			assert.Equal(t, tc.want, answer{lease, from})
		})
	}
}

// TestMarkerLivesOutsideTheCacheDir: the sandbox grants a run its workspace, cache dir
// included, so a marker there let a confined run rename its own lease, or drop it, for
// every later run in the checkout. A file a run writes where the marker used to be is
// nobody's binding.
func TestMarkerLivesOutsideTheCacheDir(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	cacheDir := filepath.Join(t.TempDir(), ".magus")
	require.NoError(t, os.MkdirAll(cacheDir, 0o755))

	require.NoError(t, NewStore(Location{StateBase: envStateBase(t), CacheDir: cacheDir}).Bind(Caller{}, "wave/worker"))
	assert.True(t, strings.HasPrefix(placeAt(t, cacheDir).MarkerPath(), state+string(filepath.Separator)),
		"the marker is under the state dir: %s", placeAt(t, cacheDir).MarkerPath())
	entries, err := os.ReadDir(cacheDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "binding writes nothing into the cache dir")

	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, LeaseMarkerName), []byte("wave/root\n"), 0o644))
	lease, from := ActingLease(placeAt(t, cacheDir), "")
	assert.Equal(t, "wave/worker", lease, "a marker-shaped file in the cache dir binds nothing")
	assert.Equal(t, job.LeaseSourceMarker, from)
}

// A binding is state private to its owner.
func TestBindRecordIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cacheDir := filepath.Join(t.TempDir(), ".magus")

	require.NoError(t, NewStore(Location{StateBase: envStateBase(t), CacheDir: cacheDir}).Bind(Caller{}, "wave/worker"))
	fi, err := os.Stat(placeAt(t, cacheDir).MarkerPath())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

// An identified caller's binding is the repository's, not a checkout's: a worktree of the
// same repository reads it.
func TestACallersRecordIsReadFromEveryWorktreeOfTheRepository(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	main, worktree := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(worktree, ".git"),
		[]byte(fmt.Sprintf("gitdir: %s\n", filepath.Join(main, ".git", "worktrees", "w"))), 0o644))
	spawner := NewStore(Location{StateBase: envStateBase(t), CacheDir: filepath.Join(main, ".magus"), Root: main})
	worker := NewStore(Location{StateBase: envStateBase(t), CacheDir: filepath.Join(worktree, ".magus"), Root: worktree})
	child := Caller{Host: "claude-code", Session: "s1", Agent: "a1b2c3"}

	require.NoError(t, spawner.Bind(child, "wave/worker"))
	assert.Error(t, spawner.Bind(child, "not a lease"), "an id ValidJobID rejects is refused")

	assert.Equal(t, "wave/worker", worker.Bound(child))
	for _, dir := range []string{main, worktree} {
		_, err := os.Stat(filepath.Join(dir, ".magus"))
		assert.True(t, os.IsNotExist(err), "nothing is written into %s's cache dir", dir)
	}
}

// TestNoRecordAnswersForAnotherKey is the boundary the 2026-09-26 incident crossed: a
// subagent's exec wrote a binding its parent then read. Every key reads its own record
// and nothing else, so no caller is ever graded as another.
func TestNoRecordAnswersForAnotherKey(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	s := NewStore(Location{StateBase: envStateBase(t), CacheDir: filepath.Join(root, ".magus"), Root: root})
	bound := map[string]Caller{
		"wave/child":    {Host: "claude-code", Session: "s1", Agent: "a1b2c3"},
		"wave/parent":   {Host: "claude-code", Session: "s1"},
		"wave/checkout": {Host: "opencode"},
	}
	for id, c := range bound {
		require.NoError(t, s.Bind(c, id))
	}

	got := map[string]string{}
	for name, c := range map[string]Caller{
		"the child":                  bound["wave/child"],
		"the parent":                 bound["wave/parent"],
		"an identity-less caller":    bound["wave/checkout"],
		"another identity-less host": {Host: "other"},
		"another agent":              {Host: "claude-code", Session: "s1", Agent: "d4e5f6"},
		"another session":            {Host: "claude-code", Session: "s2", Agent: "a1b2c3"},
		"another host":               {Host: "codex", Session: "s1", Agent: "a1b2c3"},
		"an agent with no session":   {Host: "claude-code", Agent: "a1b2c3"},
	} {
		got[name] = s.Bound(c)
	}
	assert.Equal(t, map[string]string{
		"the child":                  "wave/child",
		"the parent":                 "wave/parent",
		"an identity-less caller":    "wave/checkout",
		"another identity-less host": "wave/checkout",
		"another agent":              "",
		"another session":            "",
		"another host":               "",
		"an agent with no session":   "",
	}, got)
	lease, from := ActingLease(placeAt(t, filepath.Join(root, ".magus")), "")
	assert.Equal(t, []any{"wave/checkout", job.LeaseSourceMarker}, []any{lease, from},
		"a process that is not a hook reads the checkout's record")
}

// Only the guard writes a record, through a rename, so one that does not read is damage
// from outside, and it reads as no binding rather than as a refusal nothing could clear.
func TestARecordThatDoesNotReadIsNone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cacheDir := t.TempDir()
	s := NewStore(Location{StateBase: envStateBase(t), CacheDir: cacheDir})

	writeMarker(t, cacheDir, "not a lease id!\n")
	assert.Empty(t, s.Bound(Caller{}))
	lease, from := ActingLease(placeAt(t, cacheDir), "wave/claim")
	assert.Equal(t, []any{"wave/claim", job.LeaseSourceEnv}, []any{lease, from})

	require.NoError(t, s.Bind(Caller{}, "wave/worker"), "a binding replaces what did not read")
	assert.Equal(t, "wave/worker", s.Bound(Caller{}))
}

// The read that ends a job whose checkout is gone also tombstones every caller record
// naming such a job, or naming a row nobody holds any more, so that caller reads as bound
// to an ended job rather than unbound. A record naming a job still taken somewhere, or one
// never taken, stays.
func TestListTombstonesRecordsWhoseCheckoutIsGone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, _ := sweepStore(t, 1_000, time.Hour)
	gone, here := t.TempDir(), t.TempDir()
	plant(t, s,
		job.Job{ID: "w/gone", State: job.StateRunning, CheckoutRoot: gone, Registered: 900, Created: 900, Updated: 900},
		job.Job{ID: "w/returned", State: job.StateExited, CheckoutRoot: gone, Registered: 900, Created: 900, Updated: 900},
		job.Job{ID: "w/here", State: job.StateRunning, CheckoutRoot: here, Registered: 900, Created: 900, Updated: 900},
		job.Job{ID: "w/untaken", State: job.StateDeclared, Created: 900, Updated: 900},
	)
	callers := map[string]Caller{
		"w/gone":     {Host: "claude-code", Session: "s1", Agent: "a1"},
		"w/returned": {Host: "claude-code", Session: "s1", Agent: "a2"},
		"w/here":     {Host: "claude-code", Session: "s1", Agent: "a3"},
		"w/untaken":  {Host: "claude-code", Session: "s1", Agent: "a4"},
		"w/nobody":   {Host: "claude-code", Session: "s1", Agent: "a5"},
	}
	for id, c := range callers {
		require.NoError(t, s.Bind(c, id))
	}
	require.NoError(t, os.RemoveAll(gone))

	_, err := s.List(t.Context())
	require.NoError(t, err)

	type record struct {
		Bound   string
		Binding Binding
	}
	got := map[string]record{}
	for id, c := range callers {
		got[id] = record{s.Bound(c), s.Binding(c)}
	}
	assert.Equal(t, map[string]record{
		"w/gone":     {"", Binding{Job: "w/gone", Gone: true, swept: 1_000}},
		"w/returned": {"", Binding{Job: "w/returned", Gone: true, swept: 1_000}},
		"w/here":     {"w/here", Binding{Job: "w/here"}},
		"w/untaken":  {"w/untaken", Binding{Job: "w/untaken"}},
		"w/nobody":   {"", Binding{Job: "w/nobody", Gone: true, swept: 1_000}},
	}, got)
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(s.path), agentRecordDir))
	require.NoError(t, err)
	assert.Len(t, entries, 5, "the sweep leaves one record per caller and nothing beside them")
}

// A tombstone lasts jobs.stale_after from the sweep that wrote it, and then the caller
// reads as unbound. A jobs.stale_after of 0 ends nothing, so it keeps every tombstone.
func TestListPrunesTombstonesAfterStaleAfter(t *testing.T) {
	for name, tc := range map[string]struct {
		staleAfter time.Duration
		want       map[string]Binding
	}{
		"an hour": {time.Hour, map[string]Binding{
			"expired": {},
			"fresh":   {Job: "w/fresh", Gone: true, swept: 4_601},
		}},
		"zero": {0, map[string]Binding{
			"expired": {Job: "w/expired", Gone: true, swept: 4_600},
			"fresh":   {Job: "w/fresh", Gone: true, swept: 4_601},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			s, _ := sweepStore(t, 8_200, tc.staleAfter)
			callers := map[string]Caller{
				"expired": {Host: "claude-code", Session: "s1", Agent: "a1"},
				"fresh":   {Host: "claude-code", Session: "s1", Agent: "a2"},
			}
			require.NoError(t, os.MkdirAll(filepath.Join(filepath.Dir(s.path), agentRecordDir), 0o700))
			require.NoError(t, os.WriteFile(s.record(callers["expired"]), []byte("gone w/expired 4600\n"), 0o600))
			require.NoError(t, os.WriteFile(s.record(callers["fresh"]), []byte("gone w/fresh 4601\n"), 0o600))

			_, err := s.List(t.Context())
			require.NoError(t, err)

			got := map[string]Binding{}
			for name, c := range callers {
				got[name] = s.Binding(c)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// A pass over the binding records reads one file per caller that ever bound, so a read
// runs one only when it ended a row, when a row the last pass saw is gone, or once
// recordSweepEvery has passed. A record naming a row that never existed waits for the
// interval; one naming a row somebody removed is tombstoned by the next read.
func TestListSweepsRecordsOncePerIntervalUnlessARowGoes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, _ := sweepStore(t, 1_000, time.Hour)
	here := t.TempDir()
	plant(t, s,
		job.Job{ID: "w/here", State: job.StateRunning, CheckoutRoot: here, Registered: 900, Created: 900, Updated: 900},
		job.Job{ID: "w/removed", State: job.StateRunning, CheckoutRoot: here, Registered: 900, Created: 900, Updated: 900},
	)
	nobody := Caller{Host: "claude-code", Session: "s1", Agent: "a1"}
	removed := Caller{Host: "claude-code", Session: "s1", Agent: "a2"}
	require.NoError(t, s.Bind(removed, "w/removed"))
	_, err := s.List(t.Context())
	require.NoError(t, err)

	require.NoError(t, s.Bind(nobody, "w/nobody"))
	_, err = s.List(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Binding{Job: "w/nobody"}, s.Binding(nobody), "inside the interval, with every row still there, no pass runs")

	_, err = s.Delete(t.Context(), "w/removed", false)
	require.NoError(t, err)
	_, err = s.List(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Binding{Job: "w/removed", Gone: true, swept: 1_000}, s.Binding(removed), "a row the last pass saw is gone")
	assert.Equal(t, Binding{Job: "w/nobody", Gone: true, swept: 1_000}, s.Binding(nobody), "the pass reads every record")

	require.NoError(t, s.Bind(nobody, "w/nobody-again"))
	s.clock = func() time.Time { return time.Unix(1_000, 0).Add(recordSweepEvery) }
	_, err = s.List(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Binding{Job: "w/nobody-again", Gone: true, swept: 1_000 + int64(recordSweepEvery/time.Second)}, s.Binding(nobody),
		"the interval runs a pass")
}

// A tombstoned caller binds again the way any caller does, and the new record replaces
// the tombstone whole.
func TestBindReplacesATombstone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, _ := sweepStore(t, 1_000, time.Hour)
	c := Caller{Host: "claude-code", Session: "s1", Agent: "a1"}
	require.NoError(t, os.MkdirAll(filepath.Join(filepath.Dir(s.path), agentRecordDir), 0o700))
	require.NoError(t, os.WriteFile(s.record(c), []byte("gone w/old 900\n"), 0o600))

	require.NoError(t, s.Bind(c, "w/next"))

	assert.Equal(t, []any{"w/next", Binding{Job: "w/next"}}, []any{s.Bound(c), s.Binding(c)})
}

// Release unbinds a caller from the job it names and from no other: a record already
// naming another job, or a tombstone, stays as it was.
func TestReleaseDropsOnlyTheNamedBinding(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, _ := sweepStore(t, 1_000, time.Hour)
	held := Caller{Host: "claude-code", Session: "s1", Agent: "a1"}
	rebound := Caller{Host: "claude-code", Session: "s1", Agent: "a2"}
	buried := Caller{Host: "claude-code", Session: "s1", Agent: "a3"}
	require.NoError(t, s.Bind(held, "w/held"))
	require.NoError(t, s.Bind(rebound, "w/next"))
	require.NoError(t, os.WriteFile(s.record(buried), []byte("gone w/held 900\n"), 0o600))

	for _, c := range []Caller{held, rebound, buried} {
		s.Release(c, "w/held")
	}
	s.Release(Caller{Host: "claude-code", Session: "s1", Agent: "never-bound"}, "w/held")

	assert.Equal(t, []Binding{{}, {Job: "w/next"}, {Job: "w/held", Gone: true, swept: 900}},
		[]Binding{s.Binding(held), s.Binding(rebound), s.Binding(buried)})
}

// A caller rebound between the sweep's read and its removal keeps the new binding.
func TestDropRecordKeepsANewBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record")
	require.NoError(t, os.WriteFile(path, []byte("w/new\n"), 0o600))

	dropRecord(path, Binding{Job: "w/old", Gone: true, swept: 900})

	assert.Equal(t, Binding{Job: "w/new"}, readBinding(path))
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the record moved aside is not left behind")
}

func TestUnintegratedBranchesSkipsWorkTheBaseHolds(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	main := repo.Git("symbolic-ref", "--short", "HEAD")
	branch := func(name string, files map[string]string) {
		repo.Git("switch", "-q", "-c", name, main)
		repo.Commit(name, files)
		repo.Git("switch", "-q", main)
	}
	branch("squashed", map[string]string{"b.txt": "b\n"})
	branch("pending", map[string]string{"c.txt": "c\n"})
	branch("named", map[string]string{"d.txt": "d\n"})
	branch("integration", map[string]string{"e.txt": "e\n"})
	repo.Commit("squash squashed", map[string]string{"b.txt": "b\n"})

	plant(t, s,
		taken(repo, job.Job{ID: "squashed", State: job.StatePass}),
		taken(repo, job.Job{ID: "pending", State: job.StateExited}),
		taken(repo, job.Job{ID: "named", State: job.StatePass}),
		taken(repo, job.Job{ID: "integration", State: job.StateNoReturn}),
		job.Job{ID: "retry", State: job.StateRunning, Integrates: []string{"named"}},
		job.Job{ID: "untaken", State: job.StateNoReturn})

	got, err := s.UnintegratedBranches(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []Unintegrated{{Job: "pending", Branch: "pending", Commits: 1}, {Job: "integration", Branch: "integration", Commits: 1}}, got,
		"a squash-merged head, one a live integration names and a row no holder took wait nowhere; an ended row's head still does")
}

// taken is row as a holder that exited it leaves it: registered, its head the
// tip of the branch named for it.
func taken(repo *testkit.Repo, row job.Job) job.Job {
	row.Registered = 1
	row.Head = tip(repo, row.ID)
	return row
}

func tip(repo *testkit.Repo, branch string) string {
	return strings.TrimSpace(repo.Git("rev-parse", branch))
}

// headed is ids as exited rows standing at their branches' tips, the rows
// [placeWith] reads. An id naming no branch has no head.
func headed(repo *testkit.Repo, ids ...string) []job.Job {
	tips := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(repo.Git("for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads")), "\n") {
		if name, id, ok := strings.Cut(line, " "); ok {
			tips[name] = id
		}
	}
	rows := make([]job.Job, len(ids))
	for i, id := range ids {
		rows[i] = job.Job{ID: id, State: job.StateExited, Registered: 1, Head: tips[id]}
	}
	return rows
}

// waitingIn is the placements off every base and change, as [Unintegrated].
func waitingIn(placed []workPlace) []Unintegrated {
	var out []Unintegrated
	for _, p := range placed {
		if p.Waiting() {
			out = append(out, Unintegrated{Job: p.Job, Branch: p.Name(), Commits: p.Commits})
		}
	}
	return out
}

// fresh is a change list read just now holding changes.
func fresh(changes ...qtypes.Change) OpenChanges {
	return OpenChanges{Fetch: &InflightFetch{Provider: "github", Base: "main", At: time.Now().Unix()}, Changes: qtypes.Changes{Base: "main", Changes: changes}}
}

// The regression the design was written for: a finished row whose branch was
// pushed, then deleted locally, still waits and still counts.
func TestAPushedRowWhoseBranchIsDeletedStillCounts(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	main := repo.Git("symbolic-ref", "--short", "HEAD")
	repo.Git("switch", "-q", "-c", "w", main)
	repo.Commit("w", map[string]string{"w.txt": "w\n"})
	repo.Git("switch", "-q", main)
	row := taken(repo, job.Job{ID: "w", State: job.StateNoReturn})
	repo.Git("update-ref", "refs/remotes/origin/w", row.Head)
	repo.Git("branch", "-q", "-D", "w")
	plant(t, s, row)
	rows, err := s.List(t.Context())
	require.NoError(t, err)

	placed, _, err := placements(t.Context(), repo.Root(), rows, fresh(), time.Now())
	require.NoError(t, err)
	require.Len(t, placed, 1)
	assert.Equal(t, placePushed, placed[0].Place)
	assert.Empty(t, placed[0].Branch, "no local name holds it")
	facts := ForkFacts(t.Context(), repo.Root(), rows, FullTest{}, fresh())
	assert.Len(t, facts.FinishedBranches, 1)
	assert.Contains(t, facts.FinishedBranches[0], "(job w, pushed, 1 commit(s))")
}

// Stacking is the answer at the limit, so rows on one open change are one place.
func TestRowsOnOneOpenChangeCountAsOnePlace(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	main := repo.Git("symbolic-ref", "--short", "HEAD")
	repo.Git("switch", "-q", "-c", "stack", main)
	var rows []job.Job
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		repo.Commit(id, map[string]string{fmt.Sprintf("stack-%s.txt", id): id + "\n"})
		rows = append(rows, job.Job{ID: id, State: job.StateExited, Registered: 1, Head: repo.Head()})
	}
	repo.Git("switch", "-q", main)
	repo.Git("switch", "-q", "-c", "alone", main)
	repo.Commit("alone", map[string]string{"alone.txt": "alone\n"})
	repo.Git("switch", "-q", main)
	rows = append(rows, taken(repo, job.Job{ID: "alone", State: job.StateExited}))
	plant(t, s, rows...)
	listed, err := s.List(t.Context())
	require.NoError(t, err)

	facts := ForkFacts(t.Context(), repo.Root(), listed, FullTest{}, fresh(qtypes.Change{ID: "988", Head: tip(repo, "stack"), Branch: "stack", Base: "main"}))

	assert.Equal(t, []string{"alone (job alone, local, 1 commit(s))", "#988 (open change, job(s) a, b, c, d, e)"}, facts.FinishedBranches)
}

// A change's branch rebased with --force-with-lease holds its rows by
// content. A change closed unmerged drops them back to pushed. A list past
// its age places none, so each counts alone.
func TestAnOpenChangeHoldsItsRowsByContent(t *testing.T) {
	t.Parallel()
	_, _, repo := worktreeStore(t)
	main := repo.Git("symbolic-ref", "--short", "HEAD")
	repo.Git("switch", "-q", "-c", "w", main)
	repo.Commit("w", map[string]string{"w.txt": "w\n"})
	repo.Git("switch", "-q", main)
	rows := headed(repo, "w")
	repo.Git("update-ref", "refs/remotes/origin/w", rows[0].Head)
	repo.Commit("main moves", map[string]string{"main.txt": "main\n"})
	repo.Git("switch", "-q", "-c", "rebased", main)
	repo.Commit("w again", map[string]string{"w.txt": "w\n"})
	repo.Git("switch", "-q", main)
	repo.Git("reset", "-q", "--hard", "HEAD~1")
	driver, why := resolveDriver(t.Context(), repo.Root())
	require.NotNil(t, driver, why)
	place := func(open OpenChanges) workPlace {
		placed, _, err := placeWith(t.Context(), driver, repo.Root(), rows, open, time.Now())
		require.NoError(t, err)
		return placed[0]
	}

	assert.Equal(t, workPlace{Job: "w", Head: rows[0].Head, Place: placeProposed, Change: "7", Branch: "w", Commits: 1},
		place(fresh(qtypes.Change{ID: "7", Head: tip(repo, "rebased")})))
	assert.Equal(t, placePushed, place(fresh()).Place, "the change closed unmerged")
	old := fresh(qtypes.Change{ID: "7", Head: tip(repo, "rebased")})
	old.Fetch.At = time.Now().Add(-2 * changeListMaxAge).Unix()
	assert.Equal(t, placePushed, place(old).Place, "an old list places no row on a change")
}

// A row stored without a head is unread, named with a fix, and counts toward
// no place. No branch name stands in for the head.
func TestARowWithNoHeadIsUnreadWithAFix(t *testing.T) {
	t.Parallel()
	s, _, repo := worktreeStore(t)
	main := repo.Git("symbolic-ref", "--short", "HEAD")
	repo.Git("switch", "-q", "-c", "old", main)
	repo.Commit("old", map[string]string{"old.txt": "old\n"})
	repo.Git("switch", "-q", main)
	plant(t, s, job.Job{ID: "old", State: job.StatePass, Registered: 1})
	rows, err := s.List(t.Context())
	require.NoError(t, err)

	facts := ForkFacts(t.Context(), repo.Root(), rows, FullTest{}, fresh())

	assert.Empty(t, facts.FinishedBranches)
	assert.Contains(t, facts.BranchesUnknown, "job old: no head is recorded: the row was stored before rows recorded a head")
	assert.Contains(t, facts.BranchesUnknown, `"left_unmerged":"<why>"`)
}

func TestForkFactsCountOnlyTheForkersOwnTree(t *testing.T) {
	t.Parallel()
	s, loc, repo := worktreeStore(t)
	main := repo.Git("symbolic-ref", "--short", "HEAD")
	peerRoot := execRepository(t).Root()
	peer := NewStore(Location{StateBase: loc.StateBase, CacheDir: loc.CacheDir, Root: peerRoot})
	for _, name := range []string{"mine-a", "mine-b", "peer-a"} {
		repo.Git("switch", "-q", "-c", name, main)
		repo.Commit(name, map[string]string{name + ".txt": name + "\n"})
		repo.Git("switch", "-q", main)
	}
	plant(t, s,
		job.Job{ID: "mine", State: job.StateRunning, Worktree: &job.Worktree{Root: t.TempDir(), Source: repo.Root()}},
		taken(repo, job.Job{ID: "mine-a", Parent: "mine", State: job.StatePass}),
		taken(repo, job.Job{ID: "mine-b", Parent: "mine", State: job.StateExited}),
		job.Job{ID: "peer", State: job.StateRunning, Worktree: &job.Worktree{Root: t.TempDir(), Source: peerRoot}},
		taken(repo, job.Job{ID: "peer-a", Parent: "peer", State: job.StatePass}))
	rows, err := s.List(t.Context())
	require.NoError(t, err)

	facts := ForkFacts(t.Context(), repo.Root(), rows, FullTest{}, fresh())
	assert.Equal(t, []string{"mine-a (job mine-a, local, 1 commit(s))", "mine-b (job mine-b, local, 1 commit(s))"}, facts.FinishedBranches,
		"the peer's branch is not this tree's")
	assert.Contains(t, facts.IntegrateRecord, `"integrates":["mine-a","mine-b"]`)
	assert.Contains(t, facts.TestUnknown, "no passed full test", "no run on record says so rather than reading as zero")
	waiting, err := s.UnintegratedBranches(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []Unintegrated{{Job: "mine-a", Branch: "mine-a", Commits: 1}, {Job: "mine-b", Branch: "mine-b", Commits: 1}}, waiting)

	peerRows, err := peer.List(t.Context())
	require.NoError(t, err)
	assert.NotContains(t, ForkFacts(t.Context(), peerRoot, peerRows, FullTest{}, fresh()).FinishedBranches, "mine-a (job mine-a, local, 1 commit(s))",
		"another tree's backlog is not the peer's")
}

func TestForkFactsCountCommitsSinceTheLastFullTest(t *testing.T) {
	t.Parallel()
	_, _, repo := worktreeStore(t)
	tested := strings.TrimSpace(repo.Git("rev-parse", "HEAD"))
	repo.Commit("one", map[string]string{"one.txt": "1\n"})
	repo.Commit("two", map[string]string{"two.txt": "2\n"})

	facts := ForkFacts(t.Context(), repo.Root(), nil, FullTest{Ref: "out1", Revision: tested}, fresh())
	assert.Equal(t, 2, facts.CommitsSinceTest)
	assert.Empty(t, facts.TestUnknown)
	assert.Contains(t, ForkFactsLine(facts), fmt.Sprintf("2 commit(s) since the last full test (out1 at %s)", tested))
	assert.Contains(t, ForkFactsLine(facts), "0 places hold work off the base")

	gone := ForkFacts(t.Context(), repo.Root(), nil, FullTest{Ref: "out1", Revision: "0000000000000000000000000000000000000000"}, fresh())
	assert.NotEmpty(t, gone.TestUnknown, "a revision the history lost is unknown, not zero")
}

// failedMissing fails every range read a content read runs.
type failedMissing struct {
	types.VCSDriver
}

func (failedMissing) RangeFiles(context.Context, string, string, string, []string) ([]string, error) {
	return nil, errors.New("git merge-base: bad object")
}

func TestAnUnreadBranchIsUnreadNotMerged(t *testing.T) {
	t.Parallel()
	_, _, repo := worktreeStore(t)
	main := repo.Git("symbolic-ref", "--short", "HEAD")
	repo.Git("switch", "-q", "-c", "pending", main)
	repo.Commit("pending", map[string]string{"pending.txt": "pending\n"})
	repo.Git("switch", "-q", main)
	finished := headed(repo, "pending")
	driver, why := resolveDriver(t.Context(), repo.Root())
	require.NotNil(t, driver, why)

	got, _, err := placeWith(t.Context(), failedMissing{VCSDriver: driver}, repo.Root(), finished, fresh(), time.Now())
	require.NoError(t, err)

	require.Len(t, got, 1)
	assert.Equal(t, placeUnread, got[0].Place)
	assert.Zero(t, got[0].Commits)
	assert.Contains(t, got[0].Why, "git merge-base: bad object", "the unread state names the failed read")

	facts := withPlaces(job.ForkFacts{}, append(got, workPlace{Job: "done", Branch: "done", Place: placeLocal, Commits: 2}))
	assert.Equal(t, []string{"done (job done, local, 2 commit(s))"}, facts.FinishedBranches, "an unread row is not unmerged")
	assert.Contains(t, facts.BranchesUnknown, "1 finished row(s) unread, so not counted: job pending")
	assert.Contains(t, facts.IntegrateRecord, `"integrates":["done"]`)
	line := ForkFactsLine(facts)
	assert.Contains(t, line, "1 place(s) hold work off the base: done")
	assert.Contains(t, line, "bad object")
}

// countedMissing counts the content reads, one RangeFiles each.
type countedMissing struct {
	types.VCSDriver
	reads *atomic.Int64
}

func (c countedMissing) RangeFiles(ctx context.Context, dir, base, head string, paths []string) ([]string, error) {
	c.reads.Add(1)
	return c.VCSDriver.RangeFiles(ctx, dir, base, head, paths)
}

func TestASecondReadOverUnchangedTipsRunsNoRangeRead(t *testing.T) {
	t.Parallel()
	_, _, repo := worktreeStore(t)
	main := repo.Git("symbolic-ref", "--short", "HEAD")
	for _, name := range []string{"pending", "squashed", "landed"} {
		repo.Git("switch", "-q", "-c", name, main)
		repo.Commit(name, map[string]string{name + ".txt": name + "\n"})
		repo.Git("switch", "-q", main)
	}
	repo.Git("merge", "-q", "--ff-only", "landed")
	repo.Commit("squash squashed", map[string]string{"squashed.txt": "squashed\n"})
	finished := headed(repo, "pending", "squashed", "landed", "gone")
	driver, why := resolveDriver(t.Context(), repo.Root())
	require.NotNil(t, driver, why)
	var reads atomic.Int64
	counted := countedMissing{VCSDriver: driver, reads: &reads}

	first, _, err := placeWith(t.Context(), counted, repo.Root(), finished, fresh(), time.Now())
	require.NoError(t, err)
	second, _, err := placeWith(t.Context(), counted, repo.Root(), finished, fresh(), time.Now())
	require.NoError(t, err)

	want := []Unintegrated{{Job: "pending", Branch: "pending", Commits: 1}}
	assert.Equal(t, []any{want, want}, []any{waitingIn(first), waitingIn(second)})
	assert.EqualValues(t, 3, reads.Load(), "the first read asks each head once; a row with no head and every second answer cost none")
}

// IntegratedByContentSuite pins that a finished branch is integrated when its content
// reached the checkout or the remote default branch, by squash, rebase or merge.
type IntegratedByContentSuite struct {
	suite.Suite
	repo *testkit.Repo
	main string
}

func TestIntegratedByContentSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(IntegratedByContentSuite))
}

func (s *IntegratedByContentSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{"shared.txt": "1\n2\n3\n4\n5\n6\n7\n8\n9\n"})
	s.main = s.repo.Git("symbolic-ref", "--short", "HEAD")
	s.repo.Git("remote", "add", "origin", s.repo.Root())
}

func (s *IntegratedByContentSuite) branch(name string, files map[string]string) {
	s.repo.Git("switch", "-q", "-c", name, s.main)
	s.repo.Commit(name, files)
	s.repo.Git("switch", "-q", s.main)
}

func (s *IntegratedByContentSuite) waiting(ids ...string) ([]Unintegrated, string) {
	driver, why := resolveDriver(s.T().Context(), s.repo.Root())
	s.Require().NotNil(driver, why)
	placed, note, err := placeWith(s.T().Context(), driver, s.repo.Root(), headed(s.repo, ids...), fresh(), time.Now())
	s.Require().NoError(err)
	return waitingIn(placed), note
}

func (s *IntegratedByContentSuite) TestABranchSquashedOnlyOnTheRemoteIsIntegrated() {
	stale := s.repo.Head()
	s.repo.Commit("main moves on", map[string]string{"main.txt": "main\n"})
	s.branch("w", map[string]string{"w.txt": "w\n"})
	s.repo.Commit("squash w", map[string]string{"w.txt": "w\n"})
	s.repo.Git("update-ref", "refs/remotes/origin/main", s.repo.Head())
	s.repo.Git("reset", "-q", "--hard", stale)

	got, note := s.waiting("w")

	s.Empty(got, "the checkout lags the remote, whose squash holds the branch's content")
	s.Empty(note)
}

// A local branch behind its own pushed copy is integrated once a base holds
// that copy. The forge squashed what was pushed.
func (s *IntegratedByContentSuite) TestABranchBehindItsMergedUpstreamIsIntegrated() {
	s.branch("w", map[string]string{"w.txt": "first\n"})
	s.repo.Git("switch", "-q", "w")
	s.repo.Commit("pushed later", map[string]string{"w.txt": "second\n"})
	s.repo.Git("update-ref", "refs/remotes/origin/w", s.repo.Head())
	s.repo.Git("reset", "-q", "--hard", "HEAD~1")
	s.repo.Git("config", "branch.w.remote", "origin")
	s.repo.Git("config", "branch.w.merge", "refs/heads/w")
	s.repo.Git("switch", "-q", s.main)
	s.repo.Commit("squash w", map[string]string{"w.txt": "second\n"})

	got, _ := s.waiting("w")

	s.Empty(got, "the pushed copy the base squashed holds the local branch")

	s.repo.Git("config", "--unset", "branch.w.merge")
	got, _ = s.waiting("w")
	s.Equal([]Unintegrated{{Job: "w", Branch: "w", Commits: 1}}, got, "an untracked branch is judged alone")
}

func (s *IntegratedByContentSuite) TestABranchNeitherBaseHoldsCountsAgainstTheNearer() {
	s.repo.Git("update-ref", "refs/remotes/origin/main", s.repo.Head())
	s.branch("w", map[string]string{"w.txt": "w\n"})

	got, _ := s.waiting("w")

	s.Equal([]Unintegrated{{Job: "w", Branch: "w", Commits: 1}}, got)
}

func (s *IntegratedByContentSuite) TestAnUnreadRemoteIsNamedAndTheCheckoutAnswers() {
	s.branch("w", map[string]string{"w.txt": "w\n"})

	got, note := s.waiting("w")

	s.Equal([]Unintegrated{{Job: "w", Branch: "w", Commits: 1}}, got)
	s.Contains(note, "origin/main does not read here")
	s.Contains(note, "only the checkout's revision")
}

func (s *IntegratedByContentSuite) TestASquashOfABranchBehindTheBaseIsIntegrated() {
	s.branch("w", map[string]string{"shared.txt": "one\n2\n3\n4\n5\n6\n7\n8\n9\n"})
	s.repo.Commit("edit bottom", map[string]string{"shared.txt": "1\n2\n3\n4\n5\n6\n7\n8\nnine\n"})
	s.repo.Commit("squash w", map[string]string{"shared.txt": "one\n2\n3\n4\n5\n6\n7\n8\nnine\n"})

	got, _ := s.waiting("w")

	s.Empty(got, "merging the branch onto the base changes nothing")
}

func (s *IntegratedByContentSuite) TestASquashTheBaseRewroteSinceIsIntegrated() {
	s.branch("w", map[string]string{"shared.txt": "one\n2\n3\n4\n5\n6\n7\n8\n9\n"})
	s.repo.Commit("squash w", map[string]string{"shared.txt": "one\n2\n3\n4\n5\n6\n7\n8\n9\n"})
	s.repo.Commit("rewrite the line", map[string]string{"shared.txt": "first\n2\n3\n4\n5\n6\n7\n8\n9\n"})

	got, _ := s.waiting("w")

	s.Empty(got, "the squash commit reads as the branch on every path it changed")
}

func (s *IntegratedByContentSuite) TestAPartlyLandedBranchIsNotIntegrated() {
	s.branch("w", map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	s.repo.Commit("land half", map[string]string{"a.txt": "a\n"})

	got, _ := s.waiting("w")

	s.Equal([]Unintegrated{{Job: "w", Branch: "w", Commits: 1}}, got)
}

func TestForkFactsTimesItselfOnTheCallClock(t *testing.T) {
	t.Parallel()
	_, _, repo := worktreeStore(t)
	var timed []string
	ctx := WithCallClock(t.Context(), func(name string) func() {
		timed = append(timed, name)
		return func() {}
	})

	ForkFacts(ctx, repo.Root(), nil, FullTest{}, OpenChanges{})

	assert.Equal(t, []string{forkFactsCall}, timed)
}

type BranchReadSuite struct {
	suite.Suite
	repo     *testkit.Repo
	finished []job.Job
}

func TestBranchReadSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(BranchReadSuite))
}

// branchFixture is a repository with n finished rows. Every third branch is missing two
// commits, every fifth is merged, and every seventh never existed.
func branchFixture(tb testkit.TB, n int) (*testkit.Repo, []job.Job) {
	repo := testkit.NewRepository(tb, map[string]string{"base.txt": "base\n"})
	var finished []job.Job
	for i := range n {
		name := fmt.Sprintf("job-%03d", i)
		row := job.Job{ID: name, State: job.StateExited, Registered: 1}
		if i%7 == 0 {
			finished = append(finished, row)
			continue
		}
		repo.Git("switch", "-q", "-c", name, "main")
		repo.Commit(name, map[string]string{name + ".txt": name + "\n"})
		if i%3 == 0 {
			repo.Commit(name+"-more", map[string]string{name + "-more.txt": "more\n"})
		}
		row.Head = repo.Head()
		finished = append(finished, row)
		repo.Git("switch", "-q", "main")
		if i%5 == 0 {
			repo.Git("merge", "-q", "--no-ff", "-m", "merge "+name, name)
		}
	}
	return repo, finished
}

// sequentialBranches reads one row at a time, the order placeWith answers in.
func sequentialBranches(ctx context.Context, driver types.VCSDriver, root string, finished []job.Job) ([]Unintegrated, error) {
	meta, err := driver.Metadata(ctx, root)
	if err != nil {
		return nil, err
	}
	var out []Unintegrated
	for _, row := range finished {
		if row.Head == "" {
			continue
		}
		_, missing, err := reached(ctx, driver, root, meta.ID, row.Head)
		if err != nil {
			continue
		}
		if missing > 0 {
			out = append(out, Unintegrated{Job: row.ID, Branch: row.ID, Commits: missing})
		}
	}
	return out, nil
}

func (s *BranchReadSuite) SetupTest() {
	s.repo, s.finished = branchFixture(s.T(), 60)
}

func (s *BranchReadSuite) TestConcurrentReadMatchesTheSequentialOneInOrder() {
	ctx := s.T().Context()
	driver, why := resolveDriver(ctx, s.repo.Root())
	s.Require().NotNil(driver, why)
	want, err := sequentialBranches(ctx, driver, s.repo.Root(), s.finished)
	s.Require().NoError(err)
	s.Require().NotEmpty(want)

	placed, _, err := placeWith(ctx, driver, s.repo.Root(), s.finished, fresh(), time.Now())
	s.Require().NoError(err)

	s.Equal(want, waitingIn(placed))
}

func (s *BranchReadSuite) TestACancelledContextIsReportedNotReadAsIntegrated() {
	ctx, cancel := context.WithCancel(s.T().Context())
	driver, why := resolveDriver(ctx, s.repo.Root())
	s.Require().NotNil(driver, why)
	cancel()

	_, _, err := placeWith(ctx, driver, s.repo.Root(), s.finished, fresh(), time.Now())

	s.Require().Error(err)
}

func BenchmarkForkFacts(b *testing.B) {
	repo, finished := branchFixture(b, 250)
	driver, why := resolveDriver(b.Context(), repo.Root())
	if driver == nil {
		b.Fatal(why)
	}
	b.ResetTimer()
	for range b.N {
		// A cold answer cache, so every row pays its range read.
		_ = os.Remove(missingCommitsPath(repo.Root()))
		if _, _, err := placeWith(b.Context(), driver, repo.Root(), finished, fresh(), time.Now()); err != nil {
			b.Fatal(err)
		}
	}
}
