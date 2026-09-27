package job

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain isolates the environment: lease markers live under XDG_STATE_HOME, and the
// parallel tests here cannot pin it one test at a time.
func TestMain(m *testing.M) { testkit.Main(m) }

// writeMarker writes content as cacheDir's checkout record.
func writeMarker(t *testing.T, cacheDir, content string) {
	t.Helper()
	path := MarkerPath(cacheDir)
	require.NotEmpty(t, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// loadableRoot writes the smallest tree WorkspaceLoadFiles finds something in, and
// returns it.
func loadableRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("jobs:\n  stale_after: 2h\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("export fun build() {}\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "job"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal", "job", "store.go"), []byte("package job\n"), 0o644))
	return root
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
	root := loadableRoot(t)
	loc := tmpLoc(t, root)
	s := NewStore(loc)
	limits := config.Jobs{}

	held, err := ForkMerge(ctx, s, "wave/worker-one", func(u *types.Job) {
		u.State, u.WritePaths = types.StateRunning, []string{"internal/job/store.go"}
	}, limits, nil)
	require.NoError(t, err)
	_, err = s.Exec(ctx, held.ID, "rev")
	require.NoError(t, err)

	_, err = ForkMerge(ctx, s, "wave/worker-two", func(u *types.Job) {
		u.State, u.WritePaths = types.StateDeclared, []string{"magusfile.buzz", "docs"}
	}, limits, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "magusfile.buzz")
	assert.Contains(t, err.Error(), "wave/worker-one")
	assert.Contains(t, err.Error(), "worktree")

	t.Run("a write path that touches no workspace-load file still forks", func(t *testing.T) {
		_, err := ForkMerge(ctx, s, "wave/worker-three", func(u *types.Job) {
			u.State, u.WritePaths = types.StateDeclared, []string{"internal/guard"}
		}, limits, nil)
		require.NoError(t, err)
	})

	t.Run("an empty checkout refuses nothing", func(t *testing.T) {
		alone := NewStore(tmpLoc(t, root))
		_, err := ForkMerge(ctx, alone, "solo", func(u *types.Job) {
			u.State, u.WritePaths = types.StateDeclared, []string{"magusfile.buzz"}
		}, limits, nil)
		require.NoError(t, err)
	})
}

// TestForkRecordsWhetherTheWritePathsWereProvenDisjoint pins the third half of the collision
// proof: the orchestrator is ADVISED to prove the write paths disjoint, and the row records
// whether they are, so a plan read later says which forks were proven and which were not.
func TestForkRecordsWhetherTheWritePathsWereProvenDisjoint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := loadableRoot(t)
	loc := tmpLoc(t, root)
	s := NewStore(loc)

	alone, err := ForkMerge(ctx, s, "wave/first", func(u *types.Job) {
		u.State, u.WritePaths = types.StateRunning, []string{"internal/job/store.go"}
	}, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, types.WriteProofAlone, alone.WriteProof, "nothing else holds the checkout")
	_, err = s.Exec(ctx, alone.ID, "rev")
	require.NoError(t, err)

	disjoint, err := ForkMerge(ctx, s, "wave/second", func(u *types.Job) {
		u.State, u.WritePaths = types.StateDeclared, []string{"internal/guard"}
	}, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, types.WriteProofDisjoint, disjoint.WriteProof)

	overlapping, err := ForkMerge(ctx, s, "wave/third", func(u *types.Job) {
		u.State, u.WritePaths = types.StateDeclared, []string{"internal/job/store.go"}
	}, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, types.WriteProofOverlapping, overlapping.WriteProof,
		"an overlap is recorded, never refused: widening a write path is the orchestrator's call")
}

// TestForkRefusesADirectoryWritePath pins MGS3018 on both doors that fork by merge: a
// directory claims every file under it, so it is declarable only as a project root the job
// owns whole, or as a directory the job creates.
func TestForkRefusesADirectoryWritePath(t *testing.T) {
	t.Parallel()

	root := loadableRoot(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "libs", "lib"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "libs", "lib", "magusfile.buzz"), []byte("export fun build() {}\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "libs", "lib", "src"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "node_modules", "pkg", "magusfile.buzz"), nil, 0o644))

	cases := []struct {
		name  string
		paths []string
		want  string // "" forks; otherwise a substring of the refusal
	}{
		{"a file", []string{"internal/job/store.go"}, ""},
		{"a project root", []string{"libs/lib"}, ""},
		{"the workspace root, itself a project", []string{"."}, ""},
		{"a directory the job creates", []string{"internal/fresh"}, ""},
		{"a file pattern", []string{"internal/job/*.go", "internal/**/*_test.go"}, ""},
		{"a wildcard subtree of a project root", []string{"libs/lib/**"}, ""},
		{"a plain directory", []string{"internal/job"}, `"internal/job", inside project "."`},
		{"a directory inside a project", []string{"libs/lib/src"}, `"libs/lib/src", inside project "libs/lib"`},
		{"a directory spelled as a glob", []string{"internal/**"}, `"internal/**" (matching the directory "internal")`},
		{"a directory pattern", []string{"libs/*/src/*"}, `(matching the directory "libs/lib/src")`},
		{"a vendored tree holding a magusfile", []string{"node_modules/pkg"}, `"node_modules/pkg"`},
		{"every bad path at once", []string{"internal", "libs/lib/src"}, `"internal", inside project "."; "libs/lib/src"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewStore(tmpLoc(t, root))
			_, err := ForkMerge(context.Background(), s, "wave/job", func(u *types.Job) {
				u.State, u.WritePaths = types.StateDeclared, tc.paths
			}, config.Jobs{}, nil)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.WritePathIsDirectory)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), "List the files the job will edit")
			rows, lerr := s.List()
			require.NoError(t, lerr)
			assert.Empty(t, rows, "a refused fork writes no row")
		})
	}

	t.Run("an update to a row that exists is not a fork", func(t *testing.T) {
		t.Parallel()
		s := NewStore(tmpLoc(t, root))
		ctx := context.Background()
		_, err := ForkMerge(ctx, s, "wave/job", func(u *types.Job) {
			u.State, u.WritePaths = types.StateDeclared, []string{"internal/job/store.go"}
		}, config.Jobs{}, nil)
		require.NoError(t, err)
		_, err = ForkMerge(ctx, s, "wave/job", func(u *types.Job) { u.Model = "opus" }, config.Jobs{}, nil)
		require.NoError(t, err)
	})
}

// TestSharedCheckoutRefusalIgnoresAJobThatIsOver is why the rule reads the STATE: a
// checkout a job that passed was taken in is a checkout nobody is working in, and
// refusing the next fork over it would strand the worktree. An exited job's holder
// returned, so its editing is over too, though the row waits on its grade.
func TestSharedCheckoutRefusalIgnoresAJobThatIsOver(t *testing.T) {
	t.Parallel()

	for _, over := range []types.JobState{types.StatePass, types.StateExited} {
		t.Run(string(over), func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			root := loadableRoot(t)
			s := NewStore(tmpLoc(t, root))

			done, err := ForkMerge(ctx, s, "wave/done", func(u *types.Job) {
				u.State, u.WritePaths = types.StateRunning, []string{"magus.yaml"}
			}, config.Jobs{}, nil)
			require.NoError(t, err)
			_, err = s.Exec(ctx, done.ID, "rev")
			require.NoError(t, err)
			_, err = s.Update(ctx, done.ID, func(u *types.Job) { u.State = over })
			require.NoError(t, err)

			next, err := ForkMerge(ctx, s, "wave/next", func(u *types.Job) {
				u.State, u.WritePaths = types.StateDeclared, []string{"magus.yaml"}
			}, config.Jobs{}, nil)
			require.NoError(t, err)
			assert.Equal(t, types.WriteProofAlone, next.WriteProof)
		})
	}
}

// TestLeaseQueryResolvesInOneOrder pins the order every caller grades by. The claim ranks
// last: it is the one answer a worker can rewrite from its own shell, so a claim that
// outranked a record would let a worker bound to one job act under another's write paths.
func TestLeaseQueryResolvesInOneOrder(t *testing.T) {
	t.Parallel()

	type answer struct {
		Lease string
		From  types.LeaseSource
	}
	for name, tc := range map[string]struct {
		query LeaseQuery
		want  answer
	}{
		"nothing answers": {LeaseQuery{}, answer{}},
		"the claim alone": {
			LeaseQuery{Claim: "wave/claim"},
			answer{"wave/claim", types.LeaseSourceEnv},
		},
		"the checkout's record alone": {
			LeaseQuery{CheckoutJob: "wave/marker"},
			answer{"wave/marker", types.LeaseSourceMarker},
		},
		"a claim that agrees with the checkout's record": {
			LeaseQuery{CheckoutJob: "wave/marker", Claim: "wave/marker"},
			answer{"wave/marker", types.LeaseSourceMarker},
		},
		"the checkout's record over a different claim": {
			LeaseQuery{CheckoutJob: "wave/marker", Claim: "wave/claim"},
			answer{"wave/marker", types.LeaseSourceContested},
		},
		"the caller's record alone": {
			LeaseQuery{CallerJob: "wave/agent"},
			answer{"wave/agent", types.LeaseSourceAgent},
		},
		"the caller's record agreeing with the claim": {
			LeaseQuery{CallerJob: "wave/agent", Claim: "wave/agent"},
			answer{"wave/agent", types.LeaseSourceAgent},
		},
		"the caller's record over a different claim": {
			LeaseQuery{CallerJob: "wave/agent", Claim: "wave/claim"},
			answer{"wave/agent", types.LeaseSourceContested},
		},
		"the flag alone": {
			LeaseQuery{Flag: "wave/flag"},
			answer{"wave/flag", types.LeaseSourceFlag},
		},
		"the flag over a different claim": {
			LeaseQuery{Flag: "wave/flag", Claim: "wave/claim"},
			answer{"wave/flag", types.LeaseSourceContested},
		},
		"the flag over everything": {
			LeaseQuery{Flag: "wave/flag", CallerJob: "wave/agent", Claim: "wave/claim"},
			answer{"wave/flag", types.LeaseSourceContested},
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
	t.Setenv("BAGGAGE", "")
	cacheDir := filepath.Join(t.TempDir(), ".magus")
	require.NoError(t, os.MkdirAll(cacheDir, 0o755))

	require.NoError(t, NewStore(Location{CacheDir: cacheDir}).Bind(Caller{}, "wave/worker"))
	assert.True(t, strings.HasPrefix(MarkerPath(cacheDir), state+string(filepath.Separator)),
		"the marker is under the state dir: %s", MarkerPath(cacheDir))
	entries, err := os.ReadDir(cacheDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "binding writes nothing into the cache dir")

	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, LeaseMarkerName), []byte("wave/root\n"), 0o644))
	lease, from := ActingLease(cacheDir, "")
	assert.Equal(t, "wave/worker", lease, "a marker-shaped file in the cache dir binds nothing")
	assert.Equal(t, types.LeaseSourceMarker, from)
}

// An identified caller's binding is the repository's, not a checkout's: a worktree of the
// same repository reads it.
func TestACallersRecordIsReadFromEveryWorktreeOfTheRepository(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	main, worktree := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(worktree, ".git"),
		[]byte("gitdir: "+filepath.Join(main, ".git", "worktrees", "w")+"\n"), 0o644))
	spawner := NewStore(Location{CacheDir: filepath.Join(main, ".magus"), Root: main})
	worker := NewStore(Location{CacheDir: filepath.Join(worktree, ".magus"), Root: worktree})
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
	s := NewStore(Location{CacheDir: filepath.Join(root, ".magus"), Root: root})
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
	lease, from := ActingLease(filepath.Join(root, ".magus"), "")
	assert.Equal(t, []any{"wave/checkout", types.LeaseSourceMarker}, []any{lease, from},
		"a process that is not a hook reads the checkout's record")
}

// Only the guard writes a record, through a rename, so one that does not read is damage
// from outside, and it reads as no binding rather than as a refusal nothing could clear.
func TestARecordThatDoesNotReadIsNone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cacheDir := t.TempDir()
	s := NewStore(Location{CacheDir: cacheDir})

	writeMarker(t, cacheDir, "not a lease id!\n")
	assert.Empty(t, s.Bound(Caller{}))
	lease, from := ActingLease(cacheDir, "wave/claim")
	assert.Equal(t, []any{"wave/claim", types.LeaseSourceEnv}, []any{lease, from})

	require.NoError(t, s.Bind(Caller{}, "wave/worker"), "a binding replaces what did not read")
	assert.Equal(t, "wave/worker", s.Bound(Caller{}))
}
