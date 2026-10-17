package types

// KnowledgeQueryOptions windows a knowledge-graph query, the opts magus\query takes. A zero
// field takes the default; a negative one is an error.
type KnowledgeQueryOptions struct {
	// Budget caps the neighborhood returned beside the matches; 0 is the default of 50.
	Budget int `json:"budget,omitempty"`
	// Limit and Offset window the matches; MatchCount stays the total. A zero Limit
	// returns every match from Offset on.
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

// KnowledgeRefsOptions windows the references of a symbol, the opts magus\refs takes.
// FileCount and RefCount stay the totals. A zero Limit returns every reference from
// Offset on; a negative field is an error.
type KnowledgeRefsOptions struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}
