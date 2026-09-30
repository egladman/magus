package job

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// declared plants one row through an unbound store, which is how an orchestrator declares
// a plan, and returns the location the worker's own store shares.
func declared(t *testing.T, rows ...types.Job) Location {
	t.Helper()

	loc := tmpLoc(t, t.TempDir())
	s := NewStore(loc)
	for _, row := range rows {
		seed(t, s, row)
	}
	return loc
}

func workerRow() types.Job {
	return types.Job{
		ID:         "adj/store",
		Criteria:   "the store is the enforcement point",
		WritePaths: []string{"internal/ledger", "types/lease.go"},
		Validation: "magus run test internal/ledger",
		State:      types.StateRunning,
	}
}

// The escape the whole rule exists to close: the guard's own denial text told a worker to
// widen its write paths with the ledger tool, and nothing checked who was running it.
func TestBoundWorkerCannotWidenItsOwnRow(t *testing.T) {
	t.Parallel()

	loc := declared(t, workerRow())
	s := boundStore(loc, "adj/store")

	_, err := s.Update(t.Context(), "adj/store", func(u *types.Job) {
		u.WritePaths = append(u.WritePaths, "internal/sessions")
	})

	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, err.Error(), "adj/store is bound to this session")
	assert.Contains(t, err.Error(), "SHRINK write_paths")
	assert.Contains(t, err.Error(), "row adj/store is what this targeted")
	assert.Contains(t, err.Error(), "report it as an unresolved risk and stop")

	after, err := NewStore(loc).List()
	require.NoError(t, err)
	assert.Equal(t, workerRow().WritePaths, after[0].WritePaths, "a refused write changes nothing")
}

// Shrinking IS the release announcement, so it has to work from inside the lease: the
// waiter on a contested path starts when the holder drops it.
func TestBoundWorkerReleasesAPath(t *testing.T) {
	t.Parallel()

	loc := declared(t, workerRow())
	s := boundStore(loc, "adj/store")

	stored, err := s.Update(t.Context(), "adj/store", func(u *types.Job) {
		u.WritePaths = []string{"internal/ledger"}
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/ledger"}, stored.WritePaths)
	require.Len(t, stored.Releases, 1)
	assert.Equal(t, "types/lease.go", stored.Releases[0].Path)
}

// A worker ends itself in fail, no_return, or exited and nowhere else. pass is a verdict
// somebody else reaches by grading evidence, and a row that could set it has no acceptance step.
func TestBoundWorkerEndsItselfInFailureOrExit(t *testing.T) {
	t.Parallel()

	loc := declared(t, workerRow())

	for _, state := range []types.JobState{types.StateFail, types.StateNoReturn, types.StateExited} {
		_, err := boundStore(loc, "adj/store").Update(t.Context(), "adj/store", func(u *types.Job) { u.State = state })
		assert.NoError(t, err, "a worker reports its own %s", state)
	}

	_, err := boundStore(loc, "adj/store").Update(t.Context(), "adj/store", func(u *types.Job) { u.State = types.StatePass })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never pass")
}

// The plan's shape is the orchestrator's. A worker that rewrites its own validation has
// chosen the check it is graded by, which is the same escape as widening the boundary.
func TestBoundWorkerCannotRewriteThePlan(t *testing.T) {
	t.Parallel()

	loc := declared(t, workerRow())
	cases := map[string]func(*types.Job){
		"validation": func(u *types.Job) { u.Validation = "magus run ci ." },
		"read_paths": func(u *types.Job) { u.ReadPaths = []string{"/"} },
		"parent":     func(u *types.Job) { u.Parent = "adj/other" },
		"model":      func(u *types.Job) { u.Model = "principal" },
		"read_only":  func(u *types.Job) { u.ReadOnly = true },
		"deadline":   func(u *types.Job) { u.Deadline = 1 << 40 },
		"goals": func(u *types.Job) {
			u.Goals = []types.CompletionGate{{ID: "gate", Check: types.LeaseCheck{Target: "ci", Project: "."}}}
		},
	}
	for field, apply := range cases {
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			_, err := boundStore(loc, "adj/store").Update(t.Context(), "adj/store", apply)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "may not change "+field)
		})
	}
}

// A holder that could loosen its own goal could grade itself done. The kind, the
// expectation and the subject were once left out of the comparison, so each one alone
// went through.
func TestBoundWorkerCannotLoosenItsGoals(t *testing.T) {
	t.Parallel()

	row := workerRow()
	row.Goals = []types.CompletionGate{{ID: "gone", Kind: types.GateKindSymbol, Expect: types.ExpectAbsent, Symbols: []string{"Legacy"}}}
	loc := declared(t, row)
	for name, loosen := range map[string]func(*types.CompletionGate){
		"kind": func(g *types.CompletionGate) {
			g.Kind, g.Symbols, g.Paths = types.GateKindPaths, nil, []string{"nothing"}
		},
		"expect":  func(g *types.CompletionGate) { g.Expect = types.ExpectPresent },
		"symbols": func(g *types.CompletionGate) { g.Symbols = []string{"NeverExisted"} },
		"paths":   func(g *types.CompletionGate) { g.Paths = []string{"x"} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := boundStore(loc, "adj/store").Update(t.Context(), "adj/store", func(u *types.Job) { loosen(&u.Goals[0]) })
			require.Error(t, err)
			assert.Contains(t, err.Error(), "may not change goals")
		})
	}
}

// A worker writes no row but its own, and the children it hands out inside its own paths.
func TestBoundWorkerWritesOnlyItsOwnRowAndChildren(t *testing.T) {
	t.Parallel()

	loc := declared(t, workerRow(), types.Job{ID: "adj/other", WritePaths: []string{"internal/sessions"}})

	_, err := boundStore(loc, "adj/store").Update(t.Context(), "adj/other", func(u *types.Job) {
		u.Criteria = "rewritten by a neighbour"
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no row but its own")

	_, err = boundStore(loc, "adj/store").Update(t.Context(), "adj/store/child", func(u *types.Job) {
		u.Parent = "adj/store"
		u.WritePaths = []string{"internal/ledger"}
	})
	assert.NoError(t, err, "a child inside the parent's own paths")

	_, err = boundStore(loc, "adj/store").Update(t.Context(), "adj/store/wide", func(u *types.Job) {
		u.Parent = "adj/store"
		u.WritePaths = []string{"internal/ledger", "cmd/magus"}
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only be handed paths its parent owns")

	_, err = boundStore(loc, "adj/store").Update(t.Context(), "adj/orphan", func(u *types.Job) {
		u.WritePaths = []string{"internal/ledger"}
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must name adj/store as its parent")
}

// Clearing drops rows the caller did not write, including the one grading it.
func TestBoundWorkerCannotClearTheLedger(t *testing.T) {
	t.Parallel()

	loc := declared(t, workerRow())

	_, err := boundStore(loc, "adj/store").Clear(t.Context())
	require.Error(t, err)
	rows, err := NewStore(loc).List()
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}

// Spawning is not a way around the boundary: a child holds no path its parent does not.
func TestChildCarriesEveryWritePathOfItsParent(t *testing.T) {
	t.Parallel()

	parent := types.Job{
		ID:         "adj/store",
		WritePaths: []string{"internal/ledger", "types/lease.go"},
		DenyPaths:  []string{"MAGUS.md"},
		ReadPaths:  []string{"internal/ledger", "internal/hint"},
		State:      types.StateRunning,
	}
	loc := declared(t, parent)

	tests := []struct {
		name  string
		child types.Job
		want  string
	}{
		{
			name:  "inside every boundary",
			child: types.Job{WritePaths: []string{"internal/ledger"}, DenyPaths: []string{"MAGUS.md", "go.mod"}, ReadPaths: []string{"internal/hint"}},
		},
		{
			name:  "no focus of its own reads the paths it was handed",
			child: types.Job{WritePaths: []string{"internal/ledger"}, DenyPaths: []string{"MAGUS.md"}},
		},
		{
			name:  "a wider read boundary",
			child: types.Job{WritePaths: []string{"internal/ledger"}, DenyPaths: []string{"MAGUS.md"}, ReadPaths: []string{"/"}},
			want:  "may only read what its parent reads",
		},
		{
			name:  "a shorter deny list",
			child: types.Job{WritePaths: []string{"internal/ledger"}},
			want:  "every deny_path its parent carries",
		},
		{
			name:  "carrying a registration it never made",
			child: types.Job{WritePaths: []string{"internal/ledger"}, DenyPaths: []string{"MAGUS.md"}, ReportedBase: "abc123", Registered: 42},
		},
		{
			name:  "graded on the way out",
			child: types.Job{WritePaths: []string{"internal/ledger"}, DenyPaths: []string{"MAGUS.md"}, State: types.StatePass},
			want:  "never pass",
		},
		{
			// The guard reads a row whose check IS the gate as owning it, so a child
			// declaring one would be minting a capability its parent does not hold.
			name: "a check the parent's does not name",
			child: types.Job{
				WritePaths: []string{"internal/ledger"}, DenyPaths: []string{"MAGUS.md"},
				Check: &types.LeaseCheck{Target: types.TargetCI, Project: "."},
			},
			want: "only a lease whose parent runs the gate runs the gate",
		},
		{
			name: "a check of its own that is not the gate",
			child: types.Job{
				WritePaths: []string{"internal/ledger"}, DenyPaths: []string{"MAGUS.md"},
				Check: &types.LeaseCheck{Target: "test", Project: "internal/ledger"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			child := tt.child
			child.Parent = "adj/store"
			id := "adj/store/" + strings.ReplaceAll(tt.name, " ", "-")
			stored, err := boundStore(loc, "adj/store").Update(t.Context(), id, func(u *types.Job) { *u = child })
			if tt.want == "" {
				require.NoError(t, err)
				assert.Zero(t, stored.Registered, "a child registers for itself or not at all")
				assert.Empty(t, stored.ReportedBase)
				return
			}
			var refused *RefusedError
			require.ErrorAs(t, err, &refused)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

// The server builds one Store at startup and serves every MCP caller from it, so an actor
// frozen at construction grades a worker that bound its checkout afterwards as the server.
func TestStoreGradesTheActorItHasAtEachWrite(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")

	loc := Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: t.TempDir()}
	s := NewStore(loc)
	seed(t, s, workerRow())

	require.NoError(t, s.Bind(Caller{}, "adj/other"))

	_, err := s.Update(t.Context(), "adj/store", func(u *types.Job) { u.Criteria = "rewritten" })
	var refused *RefusedError
	require.ErrorAs(t, err, &refused, "the worker bound after construction writes no row but its own")
	_, err = s.Clear(t.Context())
	require.ErrorAs(t, err, &refused)

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, workerRow().Criteria, rows[0].Criteria)
}

// A process a shared server forks for a remote caller acts as that caller or as nobody, and
// the marker saying so can only take rights away: a worker that sets it on itself to claim
// another lease is outranked by its own checkout's record.
func TestStampedLeaseOnlyDowngrades(t *testing.T) {
	for name, tc := range map[string]struct {
		record, claim, stamped string
		want                   Actor
	}{
		"unstamped, nothing claimed":         {"", "", "", Actor{}},
		"unstamped, the record wins":         {"adj/store", "adj/other", "", Actor{Lease: "adj/store"}},
		"stamped lease":                      {"", "adj/other", "1", Actor{Lease: "adj/other"}},
		"stamped absence":                    {"", "", "1", Actor{Unstamped: true}},
		"stamped absence under a record":     {"adj/store", "", "1", Actor{Unstamped: true}},
		"stamped lease against a record":     {"adj/store", "adj/other", "1", Actor{Unstamped: true}},
		"stamped lease the record agrees on": {"adj/store", "adj/store", "1", Actor{Lease: "adj/store"}},
	} {
		t.Run(name, func(t *testing.T) {
			loc := Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: t.TempDir()}
			if tc.record != "" {
				require.NoError(t, NewStore(loc).Bind(Caller{}, tc.record))
			}
			t.Setenv(trail.EnvBaggage, trail.BaggageLease+"="+tc.claim)
			t.Setenv(EnvStampedLease, tc.stamped)
			assert.Equal(t, tc.want, ActingActor(loc.CacheDir))
		})
	}
}

// An unstamped caller reads the book and writes nothing in it: every row may be some other
// caller's, and a new one would be a plan nobody can be held to.
func TestUnstampedActorReadsAndWritesNothing(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	t.Setenv(EnvStampedLease, "1")
	loc := declared(t, workerRow())
	loc.Actor = nil
	s := NewStore(loc)

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)

	for name, write := range map[string]func() error{
		"its row": func() error {
			_, err := s.Update(t.Context(), "adj/store", func(u *types.Job) { u.WritePaths = u.WritePaths[:1] })
			return err
		},
		"a new row": func() error {
			_, err := s.Update(t.Context(), "stray", func(u *types.Job) { u.Criteria = "a plan of its own" })
			return err
		},
		"an ending": func() error {
			_, err := Exit(t.Context(), s, "adj/store", nil, nil)
			return err
		},
		"exec":   func() error { _, err := s.Exec(t.Context(), "adj/store", "abc123"); return err },
		"clear":  func() error { _, err := s.Clear(t.Context()); return err },
		"delete": func() error { _, err := s.Delete(t.Context(), "adj/store", true); return err },
	} {
		err := write()
		var refused *RefusedError
		require.ErrorAs(t, err, &refused, name)
		assert.True(t, strings.HasPrefix(err.Error(), "job: this call arrived with no lease stamped on it"), "%s: %v", name, err)
		assert.Contains(t, err.Error(), "Send the lease you act under", name)
	}

	after, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, []types.Job{rows[0]}, after, "a refused write changes nothing")
}

// The row records who declared it: what the door the write came through put on its
// context (entry point, verified credential, the MCP client as host), plus the OS account.
// Before, registered_by came from an actor origin no door filled, so it held the account alone.
func TestRowRecordsTheDoorThatDeclaredIt(t *testing.T) {
	t.Parallel()

	loc := tmpLoc(t, t.TempDir())
	cred := types.Credential{Kind: types.KindStored, ID: "3fa9c1d2", Name: "connector-1", Grant: types.GrantConnector}
	ctx := trail.ContextWithHost(trail.ContextWithCredential(trail.ContextWithEntryPoint(t.Context(), types.EntryPointMCP), cred), "claude-code")
	row := workerRow()
	stored, err := NewStore(loc).Update(ctx, row.ID, func(cur *types.Job) { *cur = row })
	require.NoError(t, err)
	want := trail.LocalOrigin(t.Context())
	want.EntryPoint, want.Credential, want.Host = types.EntryPointMCP, cred, "claude-code"
	require.NotEmpty(t, want.UID, "the OS always answers with a uid")
	assert.Equal(t, want, stored.RegisteredBy, "the door's facts, plus the OS account the store read itself")

	after, err := boundStore(loc, "adj/store").Update(t.Context(), "adj/store", func(u *types.Job) {
		u.State = types.StateFail
	})
	require.NoError(t, err)
	assert.Equal(t, stored.RegisteredBy, after.RegisteredBy, "the registrant is the row's, not the last writer's")
}

// An unattributed write is an OBSERVATION the guard makes about somebody else's row, so
// it is the one write the boundary does not apply to: grading it would discard the notice
// exactly when it matters.
func TestUnattributedWriteIsRecordedAcrossLeases(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "held.go"), []byte("package held\n"), 0o644))
	loc := tmpLoc(t, root)
	loc.Actor = &Actor{}
	seed(t, NewStore(loc), types.Job{ID: "adj/other", WritePaths: []string{"held.go"}, State: types.StateRunning})

	require.NoError(t, boundStore(loc, "adj/store").RecordUnattributedWrite(t.Context(), "adj/other", "held.go"))

	rows, err := NewStore(loc).List()
	require.NoError(t, err)
	require.Len(t, rows[0].Unattributed, 1)
	assert.Equal(t, "held.go", rows[0].Unattributed[0].Path)
}

// Shrinking a whole-file lease to a declaration of that same file is a narrower boundary,
// not a wider one, so subset has to admit it even though the two spellings differ as
// strings; widening back out to the whole file is not admitted the same way.
func TestBoundWorkerNarrowsAWriteToADeclaration(t *testing.T) {
	t.Parallel()

	row := types.Job{ID: "adj/magusfile", WritePaths: []string{"magusfile.buzz"}, State: types.StateRunning}
	loc := declared(t, row)

	stored, err := boundStore(loc, "adj/magusfile").Update(t.Context(), "adj/magusfile", func(u *types.Job) {
		u.WritePaths = []string{"magusfile.buzz#lint"}
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"magusfile.buzz#lint"}, stored.WritePaths)

	_, err = boundStore(loc, "adj/magusfile").Update(t.Context(), "adj/magusfile", func(u *types.Job) {
		u.WritePaths = []string{"magusfile.buzz"}
	})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused, "widening a claimed declaration back to the whole file is not a shrink")
	assert.Contains(t, err.Error(), "SHRINK write_paths")
}

// A child may be handed one declaration of a file its parent owns whole: that is a
// narrower boundary than the parent's, the same admission [subset] makes for a worker
// shrinking its own row. A child claiming anything beyond what the parent owns is still
// refused.
func TestChildIsHandedADeclarationOfItsParentsFile(t *testing.T) {
	t.Parallel()

	parent := types.Job{ID: "adj/store", WritePaths: []string{"magusfile.buzz"}, State: types.StateRunning}
	loc := declared(t, parent)

	stored, err := boundStore(loc, "adj/store").Update(t.Context(), "adj/store/lint", func(u *types.Job) {
		*u = types.Job{Parent: "adj/store", WritePaths: []string{"magusfile.buzz#lint"}}
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"magusfile.buzz#lint"}, stored.WritePaths)

	_, err = boundStore(loc, "adj/store").Update(t.Context(), "adj/store/extra", func(u *types.Job) {
		*u = types.Job{Parent: "adj/store", WritePaths: []string{"magusfile.buzz#lint", "other.go"}}
	})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused, "a child claims more than its parent owns when it also names other.go")
	assert.Contains(t, err.Error(), "may only be handed paths its parent owns")
}
