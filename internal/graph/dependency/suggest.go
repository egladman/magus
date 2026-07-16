package dependency

import "github.com/egladman/magus/types"

// nearestProjectPath returns the registered project path closest to want by
// Levenshtein distance, or "" if no candidate is within the threshold.
func nearestProjectPath(want string, w *types.Workspace) string {
	threshold := len(want) / 3
	if threshold == 0 {
		return ""
	}
	best, bestDist := "", threshold+1
	for _, p := range w.All() {
		if d := levenshtein(want, p.Path); d < bestDist || (d == bestDist && p.Path < best) {
			best, bestDist = p.Path, d
		}
	}
	if bestDist > threshold {
		return ""
	}
	return best
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	row := make([]int, len(b)+1)
	for j := range row {
		row[j] = j
	}
	for i, ca := range a {
		prev := i + 1
		for j, cb := range b {
			cost := 1
			if ca == cb {
				cost = 0
			}
			next := row[j+1] + 1
			if d := prev + 1; d < next {
				next = d
			}
			if d := row[j] + cost; d < next {
				next = d
			}
			row[j] = prev
			prev = next
		}
		row[len(b)] = prev
	}
	return row[len(b)]
}
