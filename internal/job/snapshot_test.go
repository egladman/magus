package job

import (
	"errors"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnapshotFromContext(t *testing.T) {
	t.Parallel()

	readErr := errors.New("jobs.json: unexpected end of JSON input")
	row := func() types.Job {
		return types.Job{ID: "orchestrator/guard-facts", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}}
	}
	cases := []struct {
		name   string
		pin    *Snapshot
		want   Snapshot
		pinned bool
	}{
		{"nothing pinned", nil, Snapshot{}, false},
		{"rows", &Snapshot{Rows: []types.Job{row()}}, Snapshot{Rows: []types.Job{row()}}, true},
		{"an unreadable store", &Snapshot{Err: readErr}, Snapshot{Err: readErr}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tc.pin != nil {
				ctx = WithSnapshot(ctx, *tc.pin)
			}
			got, ok := SnapshotFromContext(ctx)
			assert.Equal(t, tc.pinned, ok)
			assert.Equal(t, tc.pinned, HasSnapshot(ctx))
			assert.Equal(t, tc.want, got)
		})
	}
}

// The guard reads the pinned rows on every hook call, so a read shares them; a reader that
// may edit what it got clones first, and its edit does not reach the pinned rows.
func TestSnapshotReadSharesAndCloneIsolates(t *testing.T) {
	t.Parallel()

	pinned := Snapshot{Rows: []types.Job{{ID: "orchestrator/guard-facts", WritePaths: []string{"internal/guard/**"}}}}
	ctx := WithSnapshot(t.Context(), pinned)

	read, ok := SnapshotFromContext(ctx)
	require.True(t, ok)
	assert.Same(t, &pinned.Rows[0], &read.Rows[0], "a read hands out the pinned rows, not a copy")

	clone := read.Clone()
	clone.Rows[0].WritePaths[0] = "edited"
	assert.Equal(t, "internal/guard/**", pinned.Rows[0].WritePaths[0], "a clone's edit does not reach the pinned rows")
}
