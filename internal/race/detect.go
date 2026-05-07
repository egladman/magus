package race

import (
	"path/filepath"
	"slices"
	"time"
)

const maxFindings = 50

// pathFilter decides whether a path is eligible for race findings.
type pathFilter interface {
	Allow(path string) bool
}

// detect correlates fs events with concurrent project intervals. A finding is
// emitted only when ≥2 projects' output snapshots both confirm writing the same
// path (attribution-gated), eliminating false positives from unrelated writes.
func detect(s snapshot, filter pathFilter) []finding {
	byPath := make(map[string][]fsEvent, len(s.events))
	for _, ev := range s.events {
		abs := filepath.Clean(ev.Path)
		if !filter.Allow(abs) {
			continue
		}
		byPath[abs] = append(byPath[abs], ev)
	}
	if len(byPath) == 0 {
		return nil
	}

	intervals := s.intervals
	slices.SortFunc(intervals, func(a, b interval) int {
		return a.StartedAt.Compare(b.StartedAt)
	})

	seen := make(map[findingKey]struct{})
	var findings []finding

	for path, evs := range byPath {
		if len(findings) >= maxFindings {
			break
		}
		for _, ev := range evs {
			concurrent := concurrentIntervals(intervals, ev.ObservedAt)
			if len(concurrent) < 2 {
				continue
			}
			// Require ≥2 projects with confirmed snapshot attribution for this path;
			// paths outside declared output dirs produce zero writers and are skipped.
			writers := confirmedWriters(path, concurrent)
			if len(writers) < 2 {
				continue
			}
			// Emit one finding per distinct same-target confirmed-writer pair.
			for i := 0; i < len(writers); i++ {
				for j := i + 1; j < len(writers); j++ {
					a, b := writers[i], writers[j]
					if a.Project == b.Project {
						continue
					}
					if a.Target != b.Target {
						continue // different targets: scheduling artifact
					}
					// Canonical order: alphabetical by project path.
					if a.Project > b.Project {
						a, b = b, a
					}
					k := findingKey{path: path, projA: a.Project, projB: b.Project, target: a.Target}
					if _, ok := seen[k]; ok {
						continue
					}
					seen[k] = struct{}{}

					ol := overlapWindow(a, b)
					findings = append(findings, finding{
						path:         path,
						projectA:     a.Project,
						projectB:     b.Project,
						target:       a.Target,
						overlapStart: ol.start,
						overlapEnd:   ol.end,
					})

					if len(findings) >= maxFindings {
						return findings
					}
				}
			}
		}
	}
	return findings
}

type findingKey struct {
	path, projA, projB, target string
}

// confirmedWriters returns intervals whose WrittenPaths snapshot contains path.
func confirmedWriters(path string, ivs []interval) []interval {
	var out []interval
	for _, iv := range ivs {
		if containsPath(iv.WrittenPaths, path) {
			out = append(out, iv)
		}
	}
	return out
}

func containsPath(paths []string, target string) bool {
	for _, p := range paths {
		if p == target {
			return true
		}
	}
	return false
}

// concurrentIntervals returns finished intervals active at t.
func concurrentIntervals(ivs []interval, t time.Time) []interval {
	var out []interval
	for _, iv := range ivs {
		if !iv.EndedAt.IsZero() && !t.Before(iv.StartedAt) && !t.After(iv.EndedAt) {
			out = append(out, iv)
		}
	}
	return out
}

type window struct {
	start, end time.Time
}

func overlapWindow(a, b interval) window {
	return window{
		start: maxTime(a.StartedAt, b.StartedAt),
		end:   minTime(a.EndedAt, b.EndedAt),
	}
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
