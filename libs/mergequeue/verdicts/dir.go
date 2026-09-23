// Package verdicts carries verdicts from validation to landing through a directory,
// which a CI system can ship between jobs as artifacts.
//
// The layout: one subdirectory per decided change, named by its id, holding VerdictFile
// and, for a green change, BundleFile. DoneFile beside them says no more will arrive.
// Every entry appears by rename, so a reader never sees a partial one; a name starting
// with "." is in progress, which is why [mergequeue.CheckID] refuses ids that start
// with one.
package verdicts

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/libs/mergequeue"
)

const (
	VerdictFile = "verdict.json"
	BundleFile  = "stage.bundle"
	DoneFile    = ".done"
)

// ExportFunc writes the commits a Lander needs for stage, from baseCommit up, into file.
type ExportFunc func(ctx context.Context, file, baseCommit, stage string) error

// Dir is both ends of one verdict directory: validation records into it and a Lander
// polls it. A Dir that polls must not be copied after its first Poll.
type Dir struct {
	Path string
	// Export writes a green verdict's stage beside it. Nil records verdicts alone.
	Export ExportFunc
	// Follow keeps polling until DoneFile appears; without it the directory is read once
	// and taken as complete.
	Follow bool

	seen map[string]bool
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
	if v.Decision == mergequeue.DecisionLand && v.Stage != "" && d.Export != nil {
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
		if _, err := os.Stat(filepath.Join(d.Path, DoneFile)); err == nil {
			done = true
		} else if !errors.Is(err, fs.ErrNotExist) {
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
