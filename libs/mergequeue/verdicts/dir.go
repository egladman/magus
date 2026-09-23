// Package verdicts carries verdicts from validation to apply through a directory,
// which a CI system can ship between jobs as artifacts.
//
// The layout: PlanFile, the plan the verdicts were decided against, and one subdirectory
// per decided change, named by its id, holding VerdictFile and, for a green change,
// BundleFile. DoneFile beside them says no more will arrive.
// Every entry appears by rename, so a reader never sees a partial one; a name starting
// with "." is in progress, which is why [mergequeue.CheckID] refuses ids that start
// with one.
package verdicts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/libs/mergequeue"
)

const (
	PlanFile    = "plan.json"
	VerdictFile = "verdict.json"
	BundleFile  = "stage.bundle"
	DoneFile    = ".done"
)

// ExportFunc writes the commits an Applier needs for stage, from baseCommit up, into file.
type ExportFunc func(ctx context.Context, file, baseCommit, stage string) error

// Dir is both ends of one verdict directory: validation records into it and an Applier
// polls it. A Dir that polls must not be copied after its first Poll.
type Dir struct {
	Path string
	// Export writes a green verdict's stage beside it. Nil records verdicts alone.
	Export ExportFunc
	// Follow keeps polling until DoneFile appears; without it the directory is read once
	// and taken as complete.
	Follow bool
	// Interval is how long [Dir.Plan] waits between reads while following.
	Interval time.Duration

	seen map[string]bool
}

// WritePlan records p as the plan the directory's verdicts are decided against. Several
// processes may write the same plan at once; a directory already holding a different
// one is an error, since its verdicts would be checked against the wrong plan.
func (d *Dir) WritePlan(p mergequeue.Plan) error {
	var want bytes.Buffer
	if err := mergequeue.WritePlan(&want, p); err != nil {
		return err
	}
	file := filepath.Join(d.Path, PlanFile)
	switch got, err := os.ReadFile(file); {
	case err == nil && bytes.Equal(got, want.Bytes()):
		return nil
	case err == nil:
		return fmt.Errorf("%s holds a different plan; validate each plan into a directory of its own", file)
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if err := os.MkdirAll(d.Path, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d.Path, "."+PlanFile+"-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed into place
	if _, err := tmp.Write(want.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

// Plan returns the plan [Dir.WritePlan] recorded, waiting for it while following. It
// returns false, and no error, when the directory is complete without one or, without
// Follow, holds none yet.
func (d *Dir) Plan(ctx context.Context) (mergequeue.Plan, bool, error) {
	return awaitPlan(ctx, filepath.Join(d.Path, PlanFile), d.Interval, func(context.Context) (bool, error) {
		if !d.Follow {
			return true, nil
		}
		return d.done()
	})
}

// awaitPlan reads file until it exists or sync reports that nothing more will arrive.
// sync runs before each read, so a read after the last sync sees everything.
func awaitPlan(ctx context.Context, file string, interval time.Duration, sync func(context.Context) (bool, error)) (mergequeue.Plan, bool, error) {
	for {
		complete, err := sync(ctx)
		if err != nil {
			return mergequeue.Plan{}, false, err
		}
		p, err := readPlan(file)
		switch {
		case err == nil:
			return p, true, nil
		case !errors.Is(err, fs.ErrNotExist):
			return mergequeue.Plan{}, false, err
		case complete:
			return mergequeue.Plan{}, false, nil
		}
		select {
		case <-ctx.Done():
			return mergequeue.Plan{}, false, ctx.Err()
		case <-time.After(max(interval, 10*time.Millisecond)):
		}
	}
}

func readPlan(file string) (mergequeue.Plan, error) {
	f, err := os.Open(file)
	if err != nil {
		return mergequeue.Plan{}, err
	}
	defer f.Close()
	p, err := mergequeue.ReadPlan(f)
	if err != nil {
		return mergequeue.Plan{}, fmt.Errorf("%s: %w", file, err)
	}
	return p, nil
}

func (d *Dir) done() (bool, error) {
	_, err := os.Stat(filepath.Join(d.Path, DoneFile))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, err
}

var (
	_ mergequeue.VerdictSink   = (*Dir)(nil)
	_ mergequeue.VerdictSource = (*Dir)(nil)
)

// Record writes v under d.Path, exporting its stage for a green change. Safe for
// concurrent use with distinct changes.
func (d *Dir) Record(ctx context.Context, v mergequeue.Verdict) error {
	if err := mergequeue.CheckID(v.Change.ID); err != nil {
		return err
	}
	if err := os.MkdirAll(d.Path, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(d.Path, "."+v.Change.ID+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) // a no-op once renamed into place
	if v.Decision == mergequeue.DecisionMerge && v.Stage != "" && d.Export != nil {
		if err := d.Export(ctx, filepath.Join(tmp, BundleFile), v.BaseCommit, v.Stage); err != nil {
			return fmt.Errorf("export stage %s: %w", v.Stage, err)
		}
	}
	if err := writeSynced(filepath.Join(tmp, VerdictFile), v); err != nil {
		return err
	}
	final := filepath.Join(d.Path, v.Change.ID)
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// writeSynced writes v and syncs it, so the rename that publishes it never publishes an
// empty file after a crash.
func writeSynced(file string, v mergequeue.Verdict) error {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if err := mergequeue.WriteVerdict(f, v); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// MarkDone writes the end-of-verdicts signal.
func (d *Dir) MarkDone() error {
	if err := os.MkdirAll(d.Path, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d.Path, DoneFile), nil, 0o644)
}

// Poll returns the verdicts that appeared since the last call.
func (d *Dir) Poll(context.Context) ([]mergequeue.Verdict, bool, error) {
	done := !d.Follow
	if !done {
		var err error
		if done, err = d.done(); err != nil {
			return nil, false, err
		}
	}
	if d.seen == nil {
		d.seen = map[string]bool{}
	}
	entries, err := os.ReadDir(d.Path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	var fresh []mergequeue.Verdict
	for _, e := range entries {
		id := e.Name()
		if !e.IsDir() || strings.HasPrefix(id, ".") || d.seen[id] {
			continue
		}
		if err := mergequeue.CheckID(id); err != nil {
			return nil, false, fmt.Errorf("%s: %w", filepath.Join(d.Path, id), err)
		}
		v, err := readVerdict(filepath.Join(d.Path, id, VerdictFile))
		if err != nil {
			return nil, false, err
		}
		if v.Change.ID != id {
			return nil, false, fmt.Errorf("%s holds the verdict on %q", filepath.Join(d.Path, id), v.Change.ID)
		}
		bundle := filepath.Join(d.Path, id, BundleFile)
		switch _, err := os.Stat(bundle); {
		case err == nil:
			v.Bundle = bundle
		case !errors.Is(err, fs.ErrNotExist):
			return nil, false, err
		}
		d.seen[id] = true
		fresh = append(fresh, v)
	}
	return fresh, done, nil
}

func readVerdict(file string) (mergequeue.Verdict, error) {
	f, err := os.Open(file)
	if err != nil {
		return mergequeue.Verdict{}, err
	}
	defer f.Close()
	v, err := mergequeue.ReadVerdict(f)
	if err != nil {
		return mergequeue.Verdict{}, fmt.Errorf("%s: %w", file, err)
	}
	return v, nil
}
