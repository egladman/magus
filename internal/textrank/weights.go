package textrank

import _ "embed"

//go:embed weights.json
var weightsJSON []byte

// Weights is every tunable number the rankers read. It comes only from the committed
// weights.json: a person edits and reviews it, and nothing adjusts it at run time.
type Weights struct {
	Version   int                `json:"version"`
	BM25F     BM25F              `json:"bm25f"`
	KindPrior map[string]float64 `json:"kind_prior"`
	PPR       PPRWeights         `json:"ppr"`
	Fusion    FusionWeights      `json:"fusion"`
	Context   ContextWeights     `json:"context"`
}

// BM25F holds the term saturation K1 and each field's weight and length normalization.
type BM25F struct {
	K1     float64                `json:"k1"`
	Fields map[string]FieldWeight `json:"fields"`
}

// FieldWeight scales a field's term frequency (Weight) and sets how strongly its length
// normalizes it (B, 0 none to 1 full).
type FieldWeight struct {
	Weight float64 `json:"weight"`
	B      float64 `json:"b"`
}

// PPRWeights configures personalized PageRank over the graph. Edges maps a relation id to
// its weight; a relation absent from Edges is not walked.
type PPRWeights struct {
	Restart    float64            `json:"restart"`
	Iterations int                `json:"iterations"`
	Tolerance  float64            `json:"tolerance"`
	MaxNodes   int                `json:"max_nodes"`
	Edges      map[string]float64 `json:"edges"`
}

// FusionWeights holds the reciprocal rank fusion constant K and each list's weight by name.
type FusionWeights struct {
	K     float64            `json:"k"`
	Lists map[string]float64 `json:"lists"`
}

// ContextWeights bounds what a task's context receives: results scoring under Floor are
// dropped, and at most TaskLimit are kept.
type ContextWeights struct {
	Floor     float64 `json:"floor"`
	TaskLimit int     `json:"task_limit"`
}

// LoadWeights decodes the embedded weights.json strictly: an unknown key, a missing
// section, a negative weight or a Restart outside (0,1) is an error naming the key.
func LoadWeights() (Weights, error) {
	panic("textrank: LoadWeights not implemented")
}

// Digest is the sha256 of w's canonical encoding, so a ranked result can name the exact
// weights that produced it.
func (w Weights) Digest() string {
	panic("textrank: Weights.Digest not implemented")
}
