package textrank

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
// descending, ties by ID ascending.
func Fuse(lists []RankedList, k float64) []Fused {
	panic("textrank: Fuse not implemented")
}
