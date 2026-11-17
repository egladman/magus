package report

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/libs/codec"
)

// DedupRecord is the -o record of `magus config history dedup`.
type DedupRecord struct {
	ReportFiles     int         `json:"report_files"`
	TotalMisses     int         `json:"total_misses"`
	UniqueKeys      int         `json:"unique_keys"`
	RedundantBuilds int         `json:"redundant_builds"`
	RedundantMs     int64       `json:"redundant_ms"`
	Approximate     bool        `json:"approximate"`
	Top             []DedupItem `json:"top"`
}

// DedupItem is one redundantly built (project, target, hash) in a DedupRecord.
type DedupItem struct {
	Project     string `json:"project"`
	Target      string `json:"target"`
	Hash        string `json:"hash"`
	ExtraBuilds int    `json:"extra_builds"`
	ExtraMs     int64  `json:"extra_ms"`
}

// PassedRecord is the -o record of `magus config history passed`.
type PassedRecord struct {
	Recorded    bool   `json:"recorded"`
	Ref         string `json:"ref"`
	Commit      string `json:"commit"`
	Target      string `json:"target"`
	Status      string `json:"status"`
	HistoryPath string `json:"history_path"`
}

// HistoryImportRecord is the -o record of `magus config history import`.
type HistoryImportRecord struct {
	Files       int    `json:"files"`
	Projects    int    `json:"projects"`
	HistoryPath string `json:"history_path"`
}

// MissEvent is one cache-miss event parsed from a shard JSONL report, tagged with
// the report File it came from so the same key seen on two shards counts as two builds.
type MissEvent struct {
	Project    string
	Target     string
	Hash       string
	DurationMs int64
	File       string
}

// ReadMissFiles reads per-shard JSONL report files (each path may be a glob) into
// one flat miss list, each event tagged with its file.
func ReadMissFiles(ctx context.Context, paths []string) ([]MissEvent, error) {
	var misses []MissEvent
	for _, p := range paths {
		matches, err := expandGlob(p)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			ms, err := readMisses(ctx, m)
			if err != nil {
				return nil, fmt.Errorf("read %q: %w", m, err)
			}
			misses = append(misses, ms...)
		}
	}
	return misses, nil
}

// expandGlob is the files p matches, or p itself when it matches none, so a
// missing literal path reports as itself.
func expandGlob(p string) ([]string, error) {
	matches, err := filepath.Glob(p)
	if err != nil {
		return nil, fmt.Errorf("bad glob %q: %w", p, err)
	}
	if len(matches) == 0 {
		matches = []string{p}
	}
	return matches, nil
}

// readMisses parses cache-miss events from one JSONL report file, each tagged with path.
func readMisses(ctx context.Context, path string) ([]MissEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []MissEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := codec.Unmarshal(line, &head); err != nil {
			continue
		}
		if head.Type != TypeTargetResult {
			continue
		}
		var ev TargetResult
		if err := codec.Unmarshal(line, &ev); err != nil {
			continue
		}
		// A "miss" is a fresh, successful run (not a cache replay) with a real duration.
		ran := ev.Status == "ok" || ev.Status == StatusPermitted
		if ev.CacheHit || !ran || ev.DurationMs <= 0 {
			continue
		}
		out = append(out, MissEvent{
			Project:    ev.Project,
			Target:     ev.Target,
			Hash:       ev.Hash,
			DurationMs: ev.DurationMs,
			File:       path,
		})
	}
	return out, sc.Err()
}

// ImportHistories folds one or more runtime-history JSON files (each input may be a
// glob) into the history at historyPath, created if absent, freshest-wins per
// (project, target). It returns how many files merged and how many projects the
// history holds after.
func ImportHistories(ctx context.Context, historyPath string, inputs []string) (files, projects int, err error) {
	// The inputs are read before the lock is taken, so decoding megabytes of shard
	// history never holds up another writer of the shared file.
	var loaded []forecast.History
	for _, p := range inputs {
		matches, err := expandGlob(p)
		if err != nil {
			return 0, 0, err
		}
		for _, m := range matches {
			var in forecast.History
			if err := in.Load(ctx, m); err != nil {
				return 0, 0, fmt.Errorf("load %q: %w", m, err)
			}
			loaded = append(loaded, in)
		}
	}
	err = forecast.UpdateHistory(ctx, historyPath, func(h *forecast.History) error {
		for i := range loaded {
			h.Merge(&loaded[i])
		}
		projects = len(h.Projects)
		return nil
	})
	return len(loaded), projects, err
}

// RecordPassed records that run's ref completed a run of its target at its commit
// with its status, in the history at historyPath.
func RecordPassed(ctx context.Context, historyPath string, run forecast.Run, now time.Time) error {
	return forecast.UpdateHistory(ctx, historyPath, func(h *forecast.History) error {
		h.RecordRun(run, now)
		return nil
	})
}
