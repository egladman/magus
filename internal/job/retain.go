package job

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/types"
)

// DefaultRetain is how long the store keeps a row that is over, and a file a clear or a
// delete archived, once nothing has touched it: a week of history to read back, while the
// file every hook call decodes stays the size of the work in flight.
const DefaultRetain = 7 * 24 * time.Hour

// retainEvery is the least time between two retention passes. A pass rides a write that
// already holds the lock and rewrites the file, so its cost is one filter over rows the
// write decoded anyway, plus one directory listing per interval.
const retainEvery = time.Hour

// strayTempAge is how old a writer's temp file in the store's directory is before a pass
// removes it. A rewrite renames its temp within milliseconds; one this old lost its writer.
const strayTempAge = time.Hour

// over reports whether nobody will act on row again: graded or ended, or exited with its
// result never collected. Server rows and rows this magus cannot write are never over.
func over(row types.Job) bool {
	if row.Holder.OrSession() == types.HolderServer || readOnly(row) != nil {
		return false
	}
	return row.State.Terminal() || row.State == types.StateExited
}

// retire removes from rows every row that is [over] and untouched for window, and returns
// what stays and how many went. A row another staying row names as its parent, an
// ancestor or a dependency stays with it, so no tree loses its root and no queue the row
// it waits on. A zero window keeps every row.
func retire(rows []types.Job, now int64, window time.Duration) ([]types.Job, int) {
	if window <= 0 {
		return rows, 0
	}
	cutoff := now - int64(window/time.Second)
	byID := make(map[string]types.Job, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	kept := map[string]bool{}
	var pending []string
	for _, r := range rows {
		if !over(r) || r.Updated > cutoff {
			kept[r.ID] = true
			pending = append(pending, r.ID)
		}
	}
	for len(pending) > 0 {
		r := byID[pending[len(pending)-1]]
		pending = pending[:len(pending)-1]
		for _, id := range append([]string{r.Parent}, r.DependsOn...) {
			if _, ok := byID[id]; ok && !kept[id] {
				kept[id] = true
				pending = append(pending, id)
			}
		}
	}
	if len(kept) == len(rows) {
		return rows, 0
	}
	out := make([]types.Job, 0, len(kept))
	for _, r := range rows {
		if kept[r.ID] {
			out = append(out, r)
		}
	}
	return out, len(rows) - len(out)
}

// retainDue runs a retention pass over f when the last one is retainEvery old: it retires
// rows (see [retire]) and removes the archives and stray temp files the window has
// outlived. A file no pass has stamped is stamped and left whole, so the first pass runs
// an interval after a store is created or first written by this magus.
func (s *Store) retainDue(f *jobsFile, now time.Time) {
	if f.Retained == 0 {
		f.Retained = now.Unix()
		return
	}
	if now.Unix()-f.Retained < int64(retainEvery/time.Second) {
		return
	}
	window := s.retainWindow()
	f.Retained = now.Unix()
	f.Jobs, _ = retire(f.Jobs, now.Unix(), window)
	s.dropExpiredFiles(now, window)
}

// retainWindow is how long a row that is over is kept.
//
// TODO: read jobs.retain from the workspace's magus.yaml once internal/config declares it.
func (s *Store) retainWindow() time.Duration {
	if s.retain != nil {
		return *s.retain
	}
	return DefaultRetain
}

// dropExpiredFiles removes from the store's directory each archive (jobs-*.json) last
// written window ago and each writer's temp file strayTempAge old. Best effort: a file
// that cannot be read or removed stays for the next pass.
func (s *Store) dropExpiredFiles(now time.Time, window time.Duration) {
	entries, err := os.ReadDir(filepath.Dir(s.path))
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		var age time.Duration
		switch {
		case strings.HasPrefix(name, "jobs-") && strings.HasSuffix(name, ".json") && window > 0:
			age = window
		case strings.HasPrefix(name, ".") && strings.Contains(name, ".tmp."):
			age = strayTempAge
		default:
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) < age {
			continue
		}
		_ = os.Remove(filepath.Join(filepath.Dir(s.path), name))
	}
}
