package hint

import (
	"strings"
	"unicode/utf8"
)

// nearest.go is the did-you-mean primitive: the edit distance every "unknown X,
// did you mean Y" site in the tree measures with, and the threshold policy the
// CLI-facing ones share. It lives here because pointing a reader at the right
// name is this package's charter, and because a caller needing a string
// function must not have to import a TUI package to get one.

// Nearest returns the closest candidate by Levenshtein distance, or "" if
// nothing is close enough.
//
// Distance is measured case-insensitively, but the candidate is returned in its
// real casing - a suggestion has to be copy-pasteable. Case counted as edits
// before, which defeated the suggestion exactly where it was most obviously
// needed: `magus run build API` misses project `api` (paths resolve exactly, and
// deliberately, since they are filesystem paths), and API->api is three
// substitutions against a threshold of two, so the user got a bare "unknown
// project" with no hint at all. Three characters of different case is not three
// typos.
func Nearest(typed string, candidates []string) string {
	if typed == "" || len(candidates) == 0 {
		return ""
	}
	folded := strings.ToLower(typed)
	best := ""
	bestDist := -1
	for _, c := range candidates {
		d := Distance(folded, strings.ToLower(c))
		if bestDist == -1 || d < bestDist {
			best = c
			bestDist = d
		}
	}
	if bestDist > Threshold(typed) {
		return ""
	}
	return best
}

// Threshold is how many edits Nearest tolerates for an input of this length: at
// most 2 for short inputs, scaling slowly. Exported so a caller doing its own
// candidate walk (one that must filter or rank on something besides distance)
// still applies the same tolerance the rest of the surface does.
func Threshold(typed string) int {
	if utf8.RuneCountInString(typed) >= 8 {
		return 3
	}
	return 2
}

// Distance is the Levenshtein edit distance between a and b, via a two-row DP
// table; suitable for short strings.
//
// It counts RUNES. These are names a human typed, and an accented or CJK
// character is one keystroke to get wrong, not the two or three bytes it
// encodes to - so a byte-counted distance rejects a suggestion that is one
// typo away.
func Distance(a, b string) int {
	if a == b {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j-1]+cost, min(curr[j-1]+1, prev[j]+1))
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
