package dependency

import (
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// nearestProjectPath returns the registered project path closest to want by
// Levenshtein distance, or "" if no candidate is within the threshold.
//
// The threshold is proportional to the input rather than hint.Threshold's flat
// 2-or-3, and the comparison is case-SENSITIVE: these are filesystem paths, so a
// deep path tolerates more slips than a bare name, and two paths differing only
// in case are two different directories.
func nearestProjectPath(want string, w *types.Workspace) string {
	threshold := len(want) / 3
	if threshold == 0 {
		return ""
	}
	best, bestDist := "", threshold+1
	for _, p := range w.All() {
		if d := hint.Distance(want, p.Path); d < bestDist || (d == bestDist && p.Path < best) {
			best, bestDist = p.Path, d
		}
	}
	if bestDist > threshold {
		return ""
	}
	return best
}
