package job

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	require.NoError(t, Checkout{CacheDir: loc.CacheDir}.Bind(held.ID))

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
	require.NoError(t, Checkout{CacheDir: loc.CacheDir}.Bind(alone.ID))

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
// checkout still carrying the marker of a job that passed is a checkout nobody is
// working in, and refusing the next fork over it would strand the worktree.
func TestSharedCheckoutRefusalIgnoresAJobThatIsOver(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := loadableRoot(t)
	loc := tmpLoc(t, root)
	s := NewStore(loc)

	done, err := ForkMerge(ctx, s, "wave/done", func(u *types.Job) {
		u.State, u.WritePaths = types.StatePass, []string{"internal/job/store.go"}
	}, config.Jobs{}, nil)
	require.NoError(t, err)
	require.NoError(t, Checkout{CacheDir: loc.CacheDir}.Bind(done.ID))

	next, err := ForkMerge(ctx, s, "wave/next", func(u *types.Job) {
		u.State, u.WritePaths = types.StateDeclared, []string{"magus.yaml"}
	}, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, types.WriteProofAlone, next.WriteProof)
}

// TestTwoSessionsInOneCheckoutEachHoldTheirOwnLease is the binding three workers sharing
// this repository could not have. The marker used to be one file per CHECKOUT, so the
// second worker's bind read the first's id, was refused as a rebind, and all three ran
// unattributed: every write-path rule fell silent at once while looking exactly like a guarded
// session.
func TestTwoSessionsInOneCheckoutEachHoldTheirOwnLease(t *testing.T) {
	t.Parallel()

	cacheDir := t.TempDir()
	one := Checkout{CacheDir: cacheDir, Session: "session-one"}
	two := Checkout{CacheDir: cacheDir, Session: "session-two"}

	require.NoError(t, one.Bind("wave/one"))
	require.NoError(t, two.Bind("wave/two"))

	assert.Equal(t, "wave/one", markerOf(t, one))
	assert.Equal(t, "wave/two", markerOf(t, two))
	assert.ElementsMatch(t, []string{"wave/one", "wave/two"}, BoundLeases(cacheDir))

	t.Run("a session bound to one lease cannot act under another", func(t *testing.T) {
		err := one.Bind("wave/two")
		require.Error(t, err)
		assert.Equal(t, "wave/one", markerOf(t, one), "the refused bind changed nothing")
		assert.NoError(t, one.Bind("wave/one"), "re-running its own bootstrap is not refused")
	})

	t.Run("vacating one leaves the sibling bound", func(t *testing.T) {
		cleared, err := one.Vacate()
		require.NoError(t, err)
		assert.Equal(t, "wave/one", cleared)
		assert.Empty(t, markerOf(t, one))
		assert.Equal(t, "wave/two", markerOf(t, two), "the sibling session still holds its own")
		assert.Equal(t, []string{"wave/two"}, BoundLeases(cacheDir))
	})
}

// TestASessionWithNoMarkerFallsBackToTheCheckout pins the documented fallback: a caller
// that reports no session, and a session nobody bound, both read the checkout-wide
// marker, which is what every binding was before this.
func TestASessionWithNoMarkerFallsBackToTheCheckout(t *testing.T) {
	t.Parallel()

	cacheDir := t.TempDir()
	require.NoError(t, BindLease(cacheDir, "wave/checkout-wide"))

	wide := Checkout{CacheDir: cacheDir}
	assert.Equal(t, "wave/checkout-wide", markerOf(t, wide), "no session reported")
	assert.Equal(t, "wave/checkout-wide", markerOf(t, Checkout{CacheDir: cacheDir, Session: "unbound"}))

	require.NoError(t, Checkout{CacheDir: cacheDir, Session: "mine"}.Bind("wave/mine"))
	assert.Equal(t, "wave/mine", markerOf(t, Checkout{CacheDir: cacheDir, Session: "mine"}),
		"a session that has its own marker never reads the checkout's")
	assert.Equal(t, "wave/checkout-wide", markerOf(t, wide), "the checkout-wide binding is untouched")
}

// TestLeaseQueryResolvesInOneOrder pins the order every caller grades by. The claim ranks
// last: it is the one answer a worker can rewrite from its own shell, so a claim that
// outranked a record would let a worker bound to one job act under another's write paths.
func TestLeaseQueryResolvesInOneOrder(t *testing.T) {
	t.Parallel()

	bound := t.TempDir()
	require.NoError(t, BindLease(bound, "wave/marker"))
	unbound := t.TempDir()

	type answer struct {
		Lease string
		From  types.LeaseSource
	}
	for name, tc := range map[string]struct {
		query LeaseQuery
		want  answer
	}{
		"nothing answers": {LeaseQuery{Checkout: Checkout{CacheDir: unbound}}, answer{}},
		"the claim alone": {
			LeaseQuery{Checkout: Checkout{CacheDir: unbound}, Claim: "wave/claim"},
			answer{"wave/claim", types.LeaseSourceEnv},
		},
		"the marker alone": {
			LeaseQuery{Checkout: Checkout{CacheDir: bound}},
			answer{"wave/marker", types.LeaseSourceMarker},
		},
		"a claim that agrees with the marker": {
			LeaseQuery{Checkout: Checkout{CacheDir: bound}, Claim: "wave/marker"},
			answer{"wave/marker", types.LeaseSourceMarker},
		},
		"the marker over a different claim": {
			LeaseQuery{Checkout: Checkout{CacheDir: bound}, Claim: "wave/claim"},
			answer{"wave/marker", types.LeaseSourceContested},
		},
		"the agent's job alone": {
			LeaseQuery{Checkout: Checkout{CacheDir: unbound}, AgentJob: "wave/agent"},
			answer{"wave/agent", types.LeaseSourceAgent},
		},
		"the agent's job agreeing with every lower source": {
			LeaseQuery{Checkout: Checkout{CacheDir: bound}, AgentJob: "wave/marker", Claim: "wave/marker"},
			answer{"wave/marker", types.LeaseSourceAgent},
		},
		"the agent's job over a different marker": {
			LeaseQuery{Checkout: Checkout{CacheDir: bound}, AgentJob: "wave/agent"},
			answer{"wave/agent", types.LeaseSourceContested},
		},
		"the flag alone": {
			LeaseQuery{Checkout: Checkout{CacheDir: unbound}, Flag: "wave/flag"},
			answer{"wave/flag", types.LeaseSourceFlag},
		},
		"the flag over a different claim": {
			LeaseQuery{Checkout: Checkout{CacheDir: unbound}, Flag: "wave/flag", Claim: "wave/claim"},
			answer{"wave/flag", types.LeaseSourceContested},
		},
		"the flag over everything": {
			LeaseQuery{Checkout: Checkout{CacheDir: bound}, Flag: "wave/flag", AgentJob: "wave/agent", Claim: "wave/claim"},
			answer{"wave/flag", types.LeaseSourceContested},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lease, from, err := tc.query.Resolve()
			require.NoError(t, err)
			assert.Equal(t, tc.want, answer{lease, from})
		})
	}
}

// TestLeaseQueryRefusesAMarkerItCannotRead pins that an unreadable binding is an error
// whatever else answers: reading it as none would hand the call to the claim, which is the
// one source a worker can rewrite.
func TestLeaseQueryRefusesAMarkerItCannotRead(t *testing.T) {
	t.Parallel()

	cacheDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, LeaseMarkerName), []byte("not a lease id!\n"), 0o644))
	for name, q := range map[string]LeaseQuery{
		"with a claim": {Checkout: Checkout{CacheDir: cacheDir}, Claim: "wave/claim"},
		"with a flag":  {Checkout: Checkout{CacheDir: cacheDir}, Flag: "wave/flag"},
		"a session":    {Checkout: Checkout{CacheDir: cacheDir, Session: "s1"}},
		"nothing else": {Checkout: Checkout{CacheDir: cacheDir}},
	} {
		lease, from, err := q.Resolve()
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "not a lease id", name)
		assert.Empty(t, lease, name)
		assert.Empty(t, from, name)
	}

	unreadable := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(unreadable, LeaseMarkerName), 0o755))
	_, _, err := LeaseQuery{Checkout: Checkout{CacheDir: unreadable}}.Resolve()
	require.Error(t, err, "a marker path that does not read as a file is not an absent marker")

	cleared, err := Checkout{CacheDir: cacheDir}.Vacate()
	require.NoError(t, err, "vacating is how the bad marker is cleared")
	assert.Empty(t, cleared)
	_, _, err = LeaseQuery{Checkout: Checkout{CacheDir: cacheDir}}.Resolve()
	assert.NoError(t, err)
}

// markerOf reads c's marker, failing the test on a marker that does not read.
func markerOf(t *testing.T, c Checkout) string {
	t.Helper()
	id, err := c.Marker()
	require.NoError(t, err)
	return id
}
