package interactive

import "strings"

// LeafScore returns a leaf-anchored match score for path against query.
// Higher is better. Returns 0 when query does not appear in path.
func LeafScore(path, query string) int {
	if query == "" {
		return 0
	}
	lcPath := strings.ToLower(path)
	lcQuery := strings.ToLower(query)
	if !strings.Contains(lcPath, lcQuery) {
		return 0
	}
	leaf := lcPath
	if i := strings.LastIndexByte(lcPath, '/'); i >= 0 {
		leaf = lcPath[i+1:]
	}
	score := 1
	if idx := strings.Index(leaf, lcQuery); idx >= 0 {
		score += 10000
		if idx == 0 {
			score += 5000
		}
		score += (len(lcQuery) * 1000) / max(len(leaf), 1)
	}
	score -= 10 * strings.Count(path, "/")
	return score
}
