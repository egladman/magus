package job

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// realSizedRows is n rows shaped like the store that made `magus job fork` take minutes
// under load: half exited with a result nobody collected, a third ended, the rest still
// held, each carrying a criteria paragraph and a result, aged across a month. Every row
// was taken in checkout, so each exited one is a landing candidate.
func realSizedRows(n int, now int64, checkout string) []types.Job {
	criteria := strings.Repeat("Address the findings in the plan's section and say which were deferred. ", 6)
	rows := make([]types.Job, n)
	for i := range rows {
		age := int64(i*2357) % (30 * 24 * 3600)
		row := types.Job{
			ID:           fmt.Sprintf("job-%04d", i),
			Criteria:     criteria,
			Checkpoint:   "08566ec817db6ff7932f0a4d62d8a9ed08f8ca18",
			Model:        "sonnet",
			Validation:   "magus run test . -- -run Store",
			Registered:   now - age,
			CheckoutRoot: checkout,
			Created:      now - age,
			Updated:      now - age,
		}
		for p := range 8 {
			row.WritePaths = append(row.WritePaths, fmt.Sprintf("internal/p%d/file%d.go", i, p))
		}
		switch r := i % 100; {
		case r < 50:
			row.State = types.StateExited
			row.Result = &types.JobResult{Job: row.ID, ChangedPaths: row.WritePaths, UnresolvedRisks: []string{criteria}}
		case r < 87:
			row.State = types.StateNoReturn
		case r < 95:
			row.State = types.StatePass
		case r < 98:
			row.State = types.StateRunning
		default:
			row.State = types.StateDeclared
		}
		if row.State.Terminal() && i > 0 && rows[i-1].State.Terminal() {
			row.Parent = rows[i-1].ID
		}
		rows[i] = row
	}
	return rows
}

// BenchmarkStoreAtRealSize reads and writes a store the size of the one that stalled
// forks: 1,100 rows as written, and the same rows after a retention pass. probes/op is the
// landing probes one read starts once the probe interval has passed, each a run of VCS
// subprocesses in a real checkout.
func BenchmarkStoreAtRealSize(b *testing.B) {
	now := time.Now().Unix()
	for _, tc := range []struct {
		name   string
		retain time.Duration
	}{
		{"as-written", 0},
		{"retained", config.Defaults().Jobs.Retain},
	} {
		s := NewStore(Location{StateBase: b.TempDir(), CacheDir: b.TempDir(), Root: b.TempDir(), Actor: &Actor{}})
		s.retain = &tc.retain
		s.clock = func() time.Time { return time.Unix(now, 0) }
		stale := 2 * time.Hour
		s.staleAfter = &stale
		probes := 0
		s.landed = func(context.Context, types.Job) string { probes++; return "" }
		rows := realSizedRows(1100, now, b.TempDir())
		// A pass long overdue, so this write runs one, as the first write past the
		// interval does on a store that has grown this large.
		if err := s.update(b.Context(), func(f *jobsFile) error { f.Jobs, f.Retained = rows, 1; return nil }); err != nil {
			b.Fatal(err)
		}

		b.Run(tc.name+"/list", func(b *testing.B) {
			probes = 0
			var n int
			for b.Loop() {
				got, err := s.List()
				if err != nil {
					b.Fatal(err)
				}
				n = len(got)
			}
			b.ReportMetric(float64(n), "rows")
			b.ReportMetric(float64(probes)/float64(b.N), "probes/op")
		})
		b.Run(tc.name+"/update", func(b *testing.B) {
			for b.Loop() {
				if _, err := s.Update(b.Context(), "job-0098", func(u *types.Job) { u.Criteria += "." }); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
