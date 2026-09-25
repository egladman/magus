package queue

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

	"github.com/egladman/magus/internal/queue/types"
)

// The entries of a [VerdictDir].
const (
	PlanFile    = "plan.json"    // the plan the verdicts were decided against
	VerdictFile = "verdict.json" // one change's verdict, in the change's subdirectory
	// CandidateBundle is the regenerated candidate beside a verdict, when validation's
	// regeneration committed anything: a bundle of that one commit on the merge beneath it.
	CandidateBundle = "candidate.bundle"
	DoneFile        = ".done" // no more verdicts will arrive
)

// VerdictDir carries verdicts from validation to apply through a directory, which a CI
// system can ship between jobs as artifacts: validation records into it and an [Applier]
// polls it. It holds [PlanFile], and per decided change a subdirectory named by its id
// holding [VerdictFile]. Every entry appears by rename, so a reader never sees a partial
// one; a name starting with "." is in progress, which is why [types.CheckID] refuses ids that
// start with one.
//
// A VerdictDir that polls must not be copied after its first Poll.
type VerdictDir struct {
	Path string
	// Follow keeps polling until DoneFile appears; without it the directory is read once
	// and taken as complete.
	Follow bool
	// Interval is how long [VerdictDir.Plan] waits between reads while following.
	Interval time.Duration

	seen map[string]bool
}

// WritePlan records p as the plan the directory's verdicts are decided against. Several
// processes may write the same plan at once; a directory already holding a different
// one is an error, since its verdicts would be checked against the wrong plan.
func (d *VerdictDir) WritePlan(p types.Plan) error {
	var want bytes.Buffer
	if err := WritePlan(&want, p); err != nil {
		return err
	}
	file := filepath.Join(d.Path, PlanFile)
	switch got, err := os.ReadFile(file); {
	case err == nil && bytes.Equal(got, want.Bytes()):
		return nil
	case err == nil:
		return fmt.Errorf("%s holds a different plan", file)
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
	if err := writeSynced(tmp, want.Bytes()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

// Plan returns the plan [VerdictDir.WritePlan] recorded, waiting for it while following. It
// returns false, and no error, when the directory is complete without one or, without
// Follow, holds none yet.
func (d *VerdictDir) Plan(ctx context.Context) (types.Plan, bool, error) {
	return awaitPlan(ctx, filepath.Join(d.Path, PlanFile), d.Interval, func(context.Context) (bool, error) {
		if !d.Follow {
			return true, nil
		}
		return d.done()
	})
}

// awaitPlan reads file until it exists or sync reports that nothing more will arrive.
// sync runs before each read, so a read after the last sync sees everything.
func awaitPlan(ctx context.Context, file string, interval time.Duration, sync func(context.Context) (bool, error)) (types.Plan, bool, error) {
	for {
		complete, err := sync(ctx)
		if err != nil {
			return types.Plan{}, false, err
		}
		p, err := ReadPlanFile(file)
		switch {
		case err == nil:
			return p, true, nil
		case !errors.Is(err, fs.ErrNotExist):
			return types.Plan{}, false, err
		case complete:
			return types.Plan{}, false, nil
		}
		select {
		case <-ctx.Done():
			return types.Plan{}, false, ctx.Err()
		case <-time.After(max(interval, 10*time.Millisecond)):
		}
	}
}

// ReadPlanFile reads and checks the [types.Plan] document in file.
func ReadPlanFile(file string) (types.Plan, error) {
	f, err := os.Open(file)
	if err != nil {
		return types.Plan{}, err
	}
	defer f.Close()
	p, err := ReadPlan(f)
	if err != nil {
		return types.Plan{}, fmt.Errorf("%s: %w", file, err)
	}
	return p, nil
}

func (d *VerdictDir) done() (bool, error) {
	_, err := os.Stat(filepath.Join(d.Path, DoneFile))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, err
}

var _ types.VerdictSource = (*VerdictDir)(nil)

// Record checks v and writes it under d.Path. Safe for concurrent use with distinct
// changes.
func (d *VerdictDir) Record(v types.Verdict) error { return d.RecordWithBundle(v, "") }

// RecordWithBundle is Record, with the file at bundle copied in beside the verdict as
// [CandidateBundle] in the same rename; "" copies none.
func (d *VerdictDir) RecordWithBundle(v types.Verdict, bundle string) error {
	var buf bytes.Buffer
	if err := writeVerdict(&buf, v); err != nil {
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
	f, err := os.OpenFile(filepath.Join(tmp, VerdictFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if err := writeSynced(f, buf.Bytes()); err != nil {
		return err
	}
	if bundle != "" {
		// Copied, not renamed: the bundle may be on another filesystem.
		content, err := os.ReadFile(bundle)
		if err != nil {
			return err
		}
		bf, err := os.OpenFile(filepath.Join(tmp, CandidateBundle), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		if err := writeSynced(bf, content); err != nil {
			return err
		}
	}
	final := filepath.Join(d.Path, v.Change.ID)
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// writeSynced writes b to f, syncs and closes it, so the rename that publishes it never
// publishes an empty file after a crash.
func writeSynced(f *os.File, b []byte) error {
	if _, err := f.Write(b); err != nil {
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
func (d *VerdictDir) MarkDone() error {
	if err := os.MkdirAll(d.Path, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d.Path, DoneFile), nil, 0o644)
}

// Poll returns the verdicts that appeared since the last call. An entry it cannot read
// is rejected for its change alone.
func (d *VerdictDir) Poll(context.Context) (types.VerdictBatch, error) {
	done := !d.Follow
	if !done {
		var err error
		if done, err = d.done(); err != nil {
			return types.VerdictBatch{}, err
		}
	}
	if d.seen == nil {
		d.seen = map[string]bool{}
	}
	entries, err := os.ReadDir(d.Path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return types.VerdictBatch{}, err
	}
	batch := types.VerdictBatch{Done: done}
	for _, e := range entries {
		id := e.Name()
		if !e.IsDir() || strings.HasPrefix(id, ".") || d.seen[id] {
			continue
		}
		d.seen[id] = true
		v, err := d.read(id)
		if err != nil {
			batch.Rejected = append(batch.Rejected, types.RejectedVerdict{Change: id, Reason: err.Error()})
			continue
		}
		batch.Verdicts = append(batch.Verdicts, v)
		bundle, err := d.bundle(id)
		switch {
		case err != nil:
			return types.VerdictBatch{}, err
		case bundle != "":
			if batch.Bundles == nil {
				batch.Bundles = map[string]string{}
			}
			batch.Bundles[id] = bundle
		}
	}
	return batch, nil
}

// bundle returns the absolute path of id's [CandidateBundle], or "" when it has none. A
// bundle that is not a regular file is none: extracting an artifact makes only those.
func (d *VerdictDir) bundle(id string) (string, error) {
	file, err := filepath.Abs(filepath.Join(d.Path, id, CandidateBundle))
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", err
	case !info.Mode().IsRegular():
		return "", nil
	}
	return file, nil
}

func (d *VerdictDir) read(id string) (types.Verdict, error) {
	if err := types.CheckID(id); err != nil {
		return types.Verdict{}, err
	}
	file := filepath.Join(d.Path, id, VerdictFile)
	f, err := os.Open(file)
	if err != nil {
		return types.Verdict{}, err
	}
	defer f.Close()
	v, err := readVerdict(f)
	if err != nil {
		return types.Verdict{}, fmt.Errorf("%s: %w", file, err)
	}
	if v.Change.ID != id {
		return types.Verdict{}, fmt.Errorf("%s holds the verdict on %q", file, v.Change.ID)
	}
	return v, nil
}
