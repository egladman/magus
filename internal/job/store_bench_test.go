package job

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
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

// benchFiles is how many Go files each benchmark checkout has edited, each in two of its
// declarations: a worker's diff, not a one-line fix.
const benchFiles = 12

// editedCheckouts is overlapCheckouts with benchFiles files edited in each checkout, and the
// changed files of the first.
func editedCheckouts(b *testing.B) (rows []types.Job, overlaps []types.JobOverlap, root, rev string, files []types.FileChange) {
	b.Helper()
	a, other, rows, _ := overlapCheckouts(b)
	tree := map[string]string{}
	for i := range benchFiles {
		tree[fmt.Sprintf("api/f%d.go", i)] = twoFuncs
	}
	rev = a.Commit("files", tree)
	other.Git("merge", "-q", "--ff-only", rev)
	for i := range benchFiles {
		p := fmt.Sprintf("api/f%d.go", i)
		a.Write(p, strings.Replace(twoFuncs, "return", "panic(1)", 2))
		other.Write(p, strings.Replace(twoFuncs, "func Y() {\n\treturn", "func Y() {\n\tpanic(2)", 1))
		files = append(files, types.FileChange{Path: p})
	}
	for i := range rows {
		rows[i].Checkpoint = rev
		rows[i].WritePaths = []string{"api/*.go"}
	}
	return rows, types.NewJobList(rows).Overlaps, a.Root(), rev, files
}

// BenchmarkRegionsSinceWarm places the changed lines of benchFiles edited files: cold reads
// them as every read did before the cache, warm answers from the cache. calls/op is the
// driver calls one read makes.
func BenchmarkRegionsSinceWarm(b *testing.B) {
	rows, _, root, rev, files := editedCheckouts(b)
	driver := gitDriver(b, root)
	cache := withFootprintCache(b.Context(), footprintCache{dir: b.TempDir()}.forRow(rows[0]))
	if _, ok, why := regionsSince(cache, driver, root, rev, files); !ok {
		b.Fatal(why)
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{{"cold", b.Context()}, {"warm", cache}} {
		b.Run(tc.name, func(b *testing.B) {
			driver.take()
			for b.Loop() {
				if _, ok, why := regionsSince(tc.ctx, driver, root, rev, files); !ok {
					b.Fatal(why)
				}
			}
			reportCalls(b, driver.take())
		})
	}
}

// BenchmarkOverlapFootprintsWarm measures one overlap between two checkouts that each edited
// benchFiles files, as `magus ls jobs` does: cold diffs both as every read did before the
// cache, warm probes each checkout and reuses its footprint.
func BenchmarkOverlapFootprintsWarm(b *testing.B) {
	rows, overlaps, root, _, _ := editedCheckouts(b)
	require.Len(b, overlaps, 1)
	driver := gitDriver(b, root)
	cache := withFootprintCache(b.Context(), footprintCache{dir: b.TempDir()})
	if got := OverlapFootprints(cache, driver, rows, overlaps); got[0].Footprint.Verdict == types.FootprintUnknown {
		b.Fatal(got[0].Footprint.Reason)
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{{"cold", b.Context()}, {"warm", cache}} {
		b.Run(tc.name, func(b *testing.B) {
			driver.take()
			for b.Loop() {
				OverlapFootprints(tc.ctx, driver, rows, overlaps)
			}
			reportCalls(b, driver.take())
		})
	}
}

func reportCalls(b *testing.B, calls map[string]int) {
	total := 0
	for _, n := range calls {
		total += n
	}
	b.ReportMetric(float64(total)/float64(b.N), "calls/op")
}
