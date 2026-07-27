package interactive

import (
	"fmt"
	"io"
)

// hintsEnabled controls actionable hints; it is unrelated to TTY detection.
var hintsEnabled = true

// SetHintsEnabled sets whether actionable hints are emitted.
func SetHintsEnabled(on bool) {
	hintsEnabled = on
}

// HintsEnabled reports whether actionable hints are active.
func HintsEnabled() bool {
	return hintsEnabled
}

// Emit writes "hint: <msg>\n" to w when hints are enabled.
func Emit(w io.Writer, msg string) {
	if !HintsEnabled() {
		return
	}
	fmt.Fprintf(w, "hint: %s\n", msg)
}

// SuggestNearest returns the closest candidate by Levenshtein distance, or "" if nothing is close enough.
func SuggestNearest(typed string, candidates []string) string {
	if typed == "" || len(candidates) == 0 {
		return ""
	}
	best := ""
	bestDist := -1
	for _, c := range candidates {
		d := levenshtein(typed, c)
		if bestDist == -1 || d < bestDist {
			best = c
			bestDist = d
		}
	}
	// Threshold: at most 2 edits for short inputs, scaling slowly.
	threshold := 2
	if len(typed) >= 8 {
		threshold = 3
	}
	if bestDist > threshold {
		return ""
	}
	return best
}

// levenshtein computes edit distance via a two-row DP table; suitable for short strings.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
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
			if a[i-1] == b[j-1] {
				cost = 0
			}
			ins := curr[j-1] + 1
			del := prev[j] + 1
			sub := prev[j-1] + cost
			m := ins
			if del < m {
				m = del
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
