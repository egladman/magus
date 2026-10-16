package textrank

import "sort"

// RankedList is one ranker's answer, best first, and the weight its ranks carry in Fuse.
type RankedList struct {
	Name   string
	Weight float64
	IDs    []string
}

// Fused is one id's combined score and its 1-based rank in each list that held it, keyed
// by list name.
type Fused struct {
	ID    string
	Score float64
	Ranks map[string]int
}

// Fuse combines ranked lists by weighted reciprocal rank fusion: an id scores the sum of
// Weight/(k+rank) over the lists holding it, rank 1-based. Results are sorted by Score
// descending, ties by ID ascending. An id repeated in one list keeps its first position.
func Fuse(lists []RankedList, k float64) []Fused {
	byID := map[string]*Fused{}
	for _, list := range lists {
		for i, id := range list.IDs {
			f := byID[id]
			if f == nil {
				f = &Fused{ID: id, Ranks: map[string]int{}}
				byID[id] = f
			}
			if _, seen := f.Ranks[list.Name]; seen {
				continue
			}
			rank := i + 1
			f.Ranks[list.Name] = rank
			f.Score += list.Weight / (k + float64(rank))
		}
	}
	out := make([]Fused, 0, len(byID))
	for _, f := range byID {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	return out
}
