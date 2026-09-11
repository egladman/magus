package ledger

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
func declared(t *testing.T, rows ...types.Lease) Location {
	t.Helper()

	loc := tmpLoc(t, t.TempDir())
	s := NewStore(loc)
	for _, row := range rows {
		seed(t, s, row)
	}
	return loc
}

func workerRow() types.Lease {
	return types.Lease{
		ID:         "adj/store",
		Goal:       "the store is the enforcement point",
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

	_, err := s.Update(t.Context(), "adj/store", func(u *types.Lease) {
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

	stored, err := s.Update(t.Context(), "adj/store", func(u *types.Lease) {
		u.WritePaths = []string{"internal/ledger"}
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/ledger"}, stored.WritePaths)
	require.Len(t, stored.Releases, 1)
	assert.Equal(t, "types/lease.go", stored.Releases[0].Path)
}

// A worker ends itself in fail or no_return and nowhere else. pass is a verdict somebody
// else reaches by grading evidence, and a row that could set it has no acceptance step.
func TestBoundWorkerEndsItselfOnlyInFailure(t *testing.T) {
	t.Parallel()

	loc := declared(t, workerRow())

	for _, state := range []types.LeaseState{types.StateFail, types.StateNoReturn} {
		_, err := boundStore(loc, "adj/store").Update(t.Context(), "adj/store", func(u *types.Lease) { u.State = state })
		assert.NoError(t, err, "a worker reports its own %s", state)
	}

	_, err := boundStore(loc, "adj/store").Update(t.Context(), "adj/store", func(u *types.Lease) { u.State = types.StatePass })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never pass")
}

// The plan's shape is the orchestrator's. A worker that rewrites its own validation has
// chosen the check it is graded by, which is the same escape as widening the lane.
func TestBoundWorkerCannotRewriteThePlan(t *testing.T) {
	t.Parallel()

	loc := declared(t, workerRow())
	cases := map[string]func(*types.Lease){
		"validation": func(u *types.Lease) { u.Validation = "magus run ci ." },
		"read_paths": func(u *types.Lease) { u.ReadPaths = []string{"/"} },
		"parent":     func(u *types.Lease) { u.Parent = "adj/other" },
		"model":      func(u *types.Lease) { u.Model = "principal" },
		"read_only":  func(u *types.Lease) { u.ReadOnly = true },
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

// A worker writes no row but its own, and the children it hands out inside its own paths.
func TestBoundWorkerWritesOnlyItsOwnRowAndChildren(t *testing.T) {
	t.Parallel()

	loc := declared(t, workerRow(), types.Lease{ID: "adj/other", WritePaths: []string{"internal/sessions"}})

	_, err := boundStore(loc, "adj/store").Update(t.Context(), "adj/other", func(u *types.Lease) {
		u.Goal = "rewritten by a neighbour"
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no row but its own")

	_, err = boundStore(loc, "adj/store").Update(t.Context(), "adj/store/child", func(u *types.Lease) {
		u.Parent = "adj/store"
		u.WritePaths = []string{"internal/ledger"}
	})
	assert.NoError(t, err, "a child inside the parent's own paths")

	_, err = boundStore(loc, "adj/store").Update(t.Context(), "adj/store/wide", func(u *types.Lease) {
		u.Parent = "adj/store"
		u.WritePaths = []string{"internal/ledger", "cmd/magus"}
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only be handed paths its parent owns")

	_, err = boundStore(loc, "adj/store").Update(t.Context(), "adj/orphan", func(u *types.Lease) {
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

// Spawning is not a way around the boundary: a child holds no lane its parent does not.
func TestChildCarriesEveryLaneOfItsParent(t *testing.T) {
	t.Parallel()

	parent := types.Lease{
		ID:         "adj/store",
		WritePaths: []string{"internal/ledger", "types/lease.go"},
		DenyPaths:  []string{"MAGUS.md"},
		ReadPaths:  []string{"internal/ledger", "internal/hint"},
		State:      types.StateRunning,
	}
	loc := declared(t, parent)

	tests := []struct {
		name  string
		child types.Lease
		want  string
	}{
		{
			name:  "inside every lane",
			child: types.Lease{WritePaths: []string{"internal/ledger"}, DenyPaths: []string{"MAGUS.md", "go.mod"}, ReadPaths: []string{"internal/hint"}},
		},
		{
			name:  "no focus of its own reads the paths it was handed",
			child: types.Lease{WritePaths: []string{"internal/ledger"}, DenyPaths: []string{"MAGUS.md"}},
		},
		{
			name:  "a wider read lane",
			child: types.Lease{WritePaths: []string{"internal/ledger"}, DenyPaths: []string{"MAGUS.md"}, ReadPaths: []string{"/"}},
			want:  "may only read what its parent reads",
		},
		{
			name:  "a shorter deny list",
			child: types.Lease{WritePaths: []string{"internal/ledger"}},
			want:  "every deny_path its parent carries",
		},
		{
			name:  "carrying a registration it never made",
			child: types.Lease{WritePaths: []string{"internal/ledger"}, DenyPaths: []string{"MAGUS.md"}, ReportedBase: "abc123", Registered: 42},
		},
		{
			name:  "graded on the way out",
			child: types.Lease{WritePaths: []string{"internal/ledger"}, DenyPaths: []string{"MAGUS.md"}, State: types.StatePass},
			want:  "never pass",
		},
		{
			// The guard reads a row whose check IS the gate as owning it, so a child
			// declaring one would be minting a capability its parent does not hold.
			name: "a check the parent's does not name",
			child: types.Lease{
				WritePaths: []string{"internal/ledger"}, DenyPaths: []string{"MAGUS.md"},
				Check: &types.LeaseCheck{Target: types.TargetCI, Project: "."},
			},
			want: "only a lease whose parent runs the gate runs the gate",
		},
		{
			name: "a check of its own that is not the gate",
			child: types.Lease{
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
			stored, err := boundStore(loc, "adj/store").Update(t.Context(), id, func(u *types.Lease) { *u = child })
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

// The daemon builds one Store at startup and serves every MCP caller from it, so an actor
// frozen at construction grades a worker that bound its checkout afterwards as the daemon.
func TestStoreGradesTheActorItHasAtEachWrite(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")

	loc := Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: t.TempDir()}
	s := NewStore(loc)
	seed(t, s, workerRow())

	require.NoError(t, BindLease(loc.CacheDir, "adj/other"))

	_, err := s.Update(t.Context(), "adj/store", func(u *types.Lease) { u.Goal = "rewritten" })
	var refused *RefusedError
	require.ErrorAs(t, err, &refused, "the worker bound after construction writes no row but its own")
	_, err = s.Clear(t.Context())
	require.ErrorAs(t, err, &refused)

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, workerRow().Goal, rows[0].Goal)
}

// The row records who declared it, which is the provenance a refusal names.
func TestRowRecordsTheSessionThatDeclaredIt(t *testing.T) {
	t.Parallel()

	loc := tmpLoc(t, t.TempDir())
	loc.Actor = &Actor{Session: "orchestrator-1", Host: "claude-code"}
	stored := seed(t, NewStore(loc), workerRow())
	assert.Equal(t, types.LeaseActor{Session: "orchestrator-1", Host: "claude-code"}, stored.RegisteredBy)

	after, err := boundStore(loc, "adj/store").Update(t.Context(), "adj/store", func(u *types.Lease) {
		u.State = types.StateFail
	})
	require.NoError(t, err)
	assert.Equal(t, stored.RegisteredBy, after.RegisteredBy, "the registrant is the row's, not the last writer's")
}

// Binding is one-way: a worker that can name another lease is graded against that lease's
// paths from its next command.
func TestBindLeaseIsOneWay(t *testing.T) {
	t.Parallel()

	cacheDir := t.TempDir()
	require.NoError(t, BindLease(cacheDir, "adj/store"))
	assert.Equal(t, "adj/store", LeaseFromMarker(cacheDir))

	err := BindLease(cacheDir, "adj/orchestrator")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "re-binding it to adj/orchestrator")
	assert.NotContains(t, err.Error(), "unresolved risk", "a refused bind wrote nothing, so there is nothing to report")
	assert.Equal(t, "adj/store", LeaseFromMarker(cacheDir), "the refused bind changed nothing")

	assert.NoError(t, BindLease(cacheDir, "adj/store"), "a worker running its bootstrap twice is not refused")
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
	seed(t, NewStore(loc), types.Lease{ID: "adj/other", WritePaths: []string{"held.go"}, State: types.StateRunning})

	require.NoError(t, boundStore(loc, "adj/store").RecordUnattributedWrite(t.Context(), "adj/other", "held.go"))

	rows, err := NewStore(loc).List()
	require.NoError(t, err)
	require.Len(t, rows[0].Unattributed, 1)
	assert.Equal(t, "held.go", rows[0].Unattributed[0].Path)
}
