package rankeval

import (
	"cmp"
	"math"
	"slices"
)

// RecallAt is the fraction of gold ids found in the first k of ranked, every grade
// counting as relevant. An id ranked twice counts once. Empty gold scores 0.
func RecallAt(ranked []string, gold map[string]int, k int) float64 {
	if len(gold) == 0 {
		return 0
	}
	seen := make(map[string]bool, len(gold))
	for _, id := range ranked[:min(k, len(ranked))] {
		if gold[id] > 0 {
			seen[id] = true
		}
	}
	return float64(len(seen)) / float64(len(gold))
}

// ReciprocalRank is 1/r for the 1-based rank r of the first gold id in ranked, or 0
// when none is ranked.
func ReciprocalRank(ranked []string, gold map[string]int) float64 {
	for i, id := range ranked {
		if gold[id] > 0 {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// NDCGAt is normalized discounted cumulative gain over the first k of ranked
// (Jarvelin and Kekalainen): gain 2^grade-1, discount log2(rank+1), normalized by the
// gain of gold sorted by grade. An id ranked twice gains once. Empty gold scores 0.
func NDCGAt(ranked []string, gold map[string]int, k int) float64 {
	seen := make(map[string]bool, len(gold))
	var dcg float64
	for i, id := range ranked[:min(k, len(ranked))] {
		if gold[id] > 0 && !seen[id] {
			seen[id] = true
			dcg += gain(gold[id]) / math.Log2(float64(i+2))
		}
	}
	grades := make([]int, 0, len(gold))
	for _, g := range gold {
		grades = append(grades, g)
	}
	slices.SortFunc(grades, func(a, b int) int { return cmp.Compare(b, a) })
	var ideal float64
	for i, g := range grades[:min(k, len(grades))] {
		ideal += gain(g) / math.Log2(float64(i+2))
	}
	if ideal == 0 {
		return 0
	}
	return dcg / ideal
}

func gain(grade int) float64 { return math.Exp2(float64(grade)) - 1 }
