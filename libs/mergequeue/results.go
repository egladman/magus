package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// The stage directory layout: one subdirectory per decided change, named by its
// path-escaped id, holding ResultFile and, for a green change, BundleFile. DoneFile
// beside them says no more will arrive. Every entry appears by rename, so a reader
// never sees a partial one; a directory whose name starts with "." is in progress.
const (
	ResultFile = "stage.json"
	BundleFile = "stage.bundle"
	DoneFile   = ".done"
)

// Exporter writes the commits landing needs for stage into file.
type Exporter func(ctx context.Context, file, stage string) error

// WriteResult records res under dir, exporting its stage commits for a green change.
func WriteResult(ctx context.Context, dir string, res StageResult, export Exporter) error {
	name := url.PathEscape(res.Change.ID)
	tmp := filepath.Join(dir, ".tmp-"+name)
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	if res.Decision == DecisionLand && res.Stage != "" && export != nil {
		if err := export(ctx, filepath.Join(tmp, BundleFile), res.Stage); err != nil {
			return fmt.Errorf("export stage %s: %w", short(res.Stage), err)
		}
	}
	if err := WriteJSON(filepath.Join(tmp, ResultFile), res); err != nil {
		return err
	}
	final := filepath.Join(dir, name)
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// MarkDone writes dir's end-of-results signal.
func MarkDone(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, DoneFile), nil, 0o644)
}

// DirResults reads verdicts from a stage directory that fills while landing runs.
type DirResults struct {
	Dir string
	// Follow keeps polling until DoneFile appears; without it the directory is read
	// once and taken as complete.
	Follow bool
	seen   map[string]StageResult
}

func (d *DirResults) Poll(context.Context) (map[string]StageResult, bool, error) {
	done := !d.Follow
	if !done {
		if _, err := os.Stat(filepath.Join(d.Dir, DoneFile)); err == nil {
			done = true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, false, err
		}
	}
	if d.seen == nil {
		d.seen = map[string]StageResult{}
	}
	entries, err := os.ReadDir(d.Dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		id, err := url.PathUnescape(e.Name())
		if err != nil {
			continue
		}
		if _, ok := d.seen[id]; ok {
			continue
		}
		res, err := ReadStageResult(filepath.Join(d.Dir, e.Name(), ResultFile))
		if err != nil {
			return nil, false, err
		}
		if res.Change.ID != id {
			return nil, false, fmt.Errorf("mergequeue: %s holds the verdict on %q", e.Name(), res.Change.ID)
		}
		if bundle := filepath.Join(d.Dir, e.Name(), BundleFile); fileExists(bundle) {
			res.Bundle = bundle
		}
		d.seen[id] = res
	}
	return d.seen, done, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
