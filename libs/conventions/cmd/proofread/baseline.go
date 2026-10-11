package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// baselineVersion is the only baseline file version proofread reads and writes.
const baselineVersion = 1

// baseline is the file -baseline names: how many findings of each rule each
// file already has, which a run tolerates.
type baseline struct {
	Version int `json:"version"`
	// Counts maps a file's path, then a rule's name, to the findings of that
	// rule in that file.
	Counts map[string]map[string]int `json:"counts"`
}

// applyBaseline returns the findings in out past the counts the baseline at
// path holds, and with prune rewrites it to this run's counts. An empty path
// returns out unchanged.
//
// A file's findings of one rule, in the order out lists them, are tolerated up
// to the baseline's count for that file and rule, and the rest are the excess
// the run reports: a text that grew a new finding at its end reports that one,
// and a file or rule the baseline does not name reports every finding. A count
// is a ceiling, so a file that improved reports nothing until -prune lowers it.
//
// With prune the baseline is rewritten to this run's counts and the result is
// empty, since a run that resets the ceiling has nothing new to report. A
// missing baseline file is created by prune and is an error without it.
func applyBaseline(path string, prune bool, out []finding) ([]finding, error) {
	if path == "" {
		if prune {
			return nil, errors.New("-prune needs -baseline")
		}

		return out, nil
	}

	if prune {
		if err := writeBaseline(path, baseline{Version: baselineVersion, Counts: countFindings(out)}); err != nil {
			return nil, err
		}

		return []finding{}, nil
	}

	b, err := readBaseline(path)
	if err != nil {
		return nil, err
	}

	seen := map[string]map[string]int{}
	excess := []finding{}

	for _, f := range out {
		file := findingPath(f)
		if seen[file] == nil {
			seen[file] = map[string]int{}
		}

		seen[file][f.Rule]++

		if seen[file][f.Rule] > b.Counts[file][f.Rule] {
			excess = append(excess, f)
		}
	}

	return excess, nil
}

// countFindings tallies out by file and rule.
func countFindings(out []finding) map[string]map[string]int {
	counts := map[string]map[string]int{}

	for _, f := range out {
		file := findingPath(f)
		if counts[file] == nil {
			counts[file] = map[string]int{}
		}

		counts[file][f.Rule]++
	}

	return counts
}

func readBaseline(path string) (baseline, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return baseline{}, fmt.Errorf("baseline %s does not exist: run with -prune to create it", path)
	}

	if err != nil {
		return baseline{}, fmt.Errorf("read baseline: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var b baseline
	if err := dec.Decode(&b); err != nil {
		return baseline{}, fmt.Errorf("read baseline %s: %w", path, err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return baseline{}, fmt.Errorf("read baseline %s: data after the object", path)
	}

	if b.Version != baselineVersion {
		return baseline{}, fmt.Errorf("baseline %s is version %d, want %d", path, b.Version, baselineVersion)
	}

	return b, nil
}

// writeBaseline writes b to path, its files and rules in sorted order so a
// rewrite changes only the counts that moved.
func writeBaseline(path string, b baseline) error {
	if b.Counts == nil {
		b.Counts = map[string]map[string]int{}
	}

	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("encode baseline: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write baseline: %w", err)
	}

	return nil
}
