package job

import (
	"errors"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// A pinned snapshot hands out copies, so a reader that edits what it got cannot change
// what the next reader under the same context sees.
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
		{"an unreadable store", &Snapshot{Err: readErr}, Snapshot{Rows: []types.Job{}, Err: readErr}, true},
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
			if len(got.Rows) > 0 {
				got.Rows[0].WritePaths[0] = "edited"
				again, _ := SnapshotFromContext(ctx)
				assert.Equal(t, tc.want, again, "a reader's edit does not reach the pinned rows")
			}
		})
	}
}
