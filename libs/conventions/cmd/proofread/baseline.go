package main

import "errors"

// applyBaseline returns the findings in out past the counts the baseline at
// path holds, and with prune rewrites it to this run's counts. An empty path
// returns out unchanged.
func applyBaseline(path string, prune bool, out []finding) ([]finding, error) {
	if path == "" {
		if prune {
			return nil, errors.New("-prune needs -baseline")
		}

		return out, nil
	}

	return nil, errors.New("-baseline is not implemented")
}
