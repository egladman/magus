package textrank

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"

	"github.com/egladman/magus/internal/json"
)

//go:embed weights.json
var weightsJSON []byte

const weightsVersion = 1

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
	K1     float64                `json:"k1"`     // 1.2: the Lucene and Robertson-Zaragoza default
	Fields map[string]FieldWeight `json:"fields"` // name 5, signature 3, path 2, doc 1, body 1: Sourcegraph weights names and definitions x5
}

// FieldWeight scales a field's term frequency (Weight) and sets how strongly its length
// normalizes it (B, 0 none to 1 full).
type FieldWeight struct {
	Weight float64 `json:"weight"`
	B      float64 `json:"b"` // 0.75: the Lucene and Robertson-Zaragoza default
}

// PPRWeights configures personalized PageRank over the graph. Edges maps a relation id to
// its weight; a relation absent from Edges is not walked.
type PPRWeights struct {
	Restart    float64            `json:"restart"`    // 0.15: one minus the 0.85 damping of Page et al. 1998
	Iterations int                `json:"iterations"` // 20: starting value, not from a paper
	Tolerance  float64            `json:"tolerance"`  // 1e-6: starting value, not from a paper
	MaxNodes   int                `json:"max_nodes"`  // 5000: starting value, not from a paper
	Edges      map[string]float64 `json:"edges"`      // no published source: starting weights to tune against internal/rankeval
}

// FusionWeights holds the reciprocal rank fusion constant K and each list's weight by name.
type FusionWeights struct {
	K     float64            `json:"k"` // 60: Cormack, Clarke and Buettcher 2009
	Lists map[string]float64 `json:"lists"`
}

// ContextWeights bounds what a task's context receives: results scoring under Floor are
// dropped, and at most TaskLimit are kept.
type ContextWeights struct {
	Floor     float64 `json:"floor"`
	TaskLimit int     `json:"task_limit"` // 8: starting value, not from a paper
}

// LoadWeights decodes the embedded weights.json strictly: an unknown key, a missing
// section or key, a negative weight, a Restart outside (0,1) or an unknown version is an
// error naming the key.
func LoadWeights() (Weights, error) {
	w, err := decodeWeights(weightsJSON)
	if err != nil {
		return Weights{}, fmt.Errorf("weights.json: %w", err)
	}
	return w, nil
}

// Digest is the sha256 of w's canonical encoding, so a ranked result can name the exact
// weights that produced it. It panics on a Weights that cannot be encoded, such as one
// holding NaN; LoadWeights never returns one.
func (w Weights) Digest() string {
	data, err := json.Marshal(w)
	if err != nil {
		panic(fmt.Sprintf("textrank: encode weights: %v", err))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func decodeWeights(data []byte) (Weights, error) {
	var head struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Weights{}, err
	}
	if head.Version != nil && *head.Version != weightsVersion {
		return Weights{}, fmt.Errorf("version: unknown version %d, this build reads %d", *head.Version, weightsVersion)
	}
	if err := checkShape(data); err != nil {
		return Weights{}, err
	}
	var w Weights
	if err := json.UnmarshalStrict(data, &w); err != nil {
		return Weights{}, err
	}
	if err := w.validate(); err != nil {
		return Weights{}, err
	}
	return w, nil
}

// checkShape names the key path of every structural fault, which the struct decoder
// cannot: it ignores a missing key and words an unknown one without its path.
func checkShape(data []byte) error {
	top, err := shapeObject(data, "", "version", "bm25f", "kind_prior", "ppr", "fusion", "context")
	if err != nil {
		return err
	}
	bm25f, err := shapeObject(top["bm25f"], "bm25f", "k1", "fields")
	if err != nil {
		return err
	}
	fields, err := shapeObject(bm25f["fields"], "bm25f.fields", FieldName, FieldSignature, FieldPath, FieldDoc, FieldBody)
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		if _, err := shapeObject(fields[name], "bm25f.fields."+name, "weight", "b"); err != nil {
			return err
		}
	}
	if _, err := shapeMembers(top["kind_prior"], "kind_prior"); err != nil {
		return err
	}
	ppr, err := shapeObject(top["ppr"], "ppr", "restart", "iterations", "tolerance", "max_nodes", "edges")
	if err != nil {
		return err
	}
	if _, err := shapeMembers(ppr["edges"], "ppr.edges"); err != nil {
		return err
	}
	fusion, err := shapeObject(top["fusion"], "fusion", "k", "lists")
	if err != nil {
		return err
	}
	if _, err := shapeMembers(fusion["lists"], "fusion.lists"); err != nil {
		return err
	}
	_, err = shapeObject(top["context"], "context", "floor", "task_limit")
	return err
}

// shapeObject returns the members of the JSON object raw, requiring exactly the keys in want.
func shapeObject(raw json.RawMessage, path string, want ...string) (map[string]json.RawMessage, error) {
	found, err := shapeMembers(raw, path)
	if err != nil {
		return nil, err
	}
	for _, key := range want {
		if _, ok := found[key]; !ok {
			return nil, fmt.Errorf("%s: missing", keyPath(path, key))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(found)) {
		if !slices.Contains(want, key) {
			return nil, fmt.Errorf("%s: unknown key", keyPath(path, key))
		}
	}
	return found, nil
}

// shapeMembers returns the members of the JSON object raw, none of them null: the struct
// decoder would read a null number as zero.
func shapeMembers(raw json.RawMessage, path string) (map[string]json.RawMessage, error) {
	var found map[string]json.RawMessage
	if err := json.Unmarshal(raw, &found); err != nil || found == nil {
		if path == "" {
			path = "weights"
		}
		return nil, fmt.Errorf("%s: must be an object", path)
	}
	for _, key := range slices.Sorted(maps.Keys(found)) {
		if bytes.Equal(bytes.TrimSpace(found[key]), []byte("null")) {
			return nil, fmt.Errorf("%s: must not be null", keyPath(path, key))
		}
	}
	return found, nil
}

func keyPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func (w Weights) validate() error {
	if w.BM25F.K1 < 0 {
		return errNegative("bm25f.k1", w.BM25F.K1)
	}
	for _, name := range slices.Sorted(maps.Keys(w.BM25F.Fields)) {
		field := w.BM25F.Fields[name]
		path := "bm25f.fields." + name
		if field.Weight < 0 {
			return errNegative(path+".weight", field.Weight)
		}
		if field.B < 0 {
			return errNegative(path+".b", field.B)
		}
		if field.B > 1 {
			return fmt.Errorf("%s.b: must be at most 1, got %v", path, field.B)
		}
	}
	if err := checkNonNegative("kind_prior", w.KindPrior); err != nil {
		return err
	}
	ppr := w.PPR
	if ppr.Restart <= 0 || ppr.Restart >= 1 {
		return fmt.Errorf("ppr.restart: must be between 0 and 1 exclusive, got %v", ppr.Restart)
	}
	if ppr.Iterations < 1 {
		return fmt.Errorf("ppr.iterations: must be at least 1, got %d", ppr.Iterations)
	}
	if ppr.Tolerance <= 0 {
		return fmt.Errorf("ppr.tolerance: must be positive, got %v", ppr.Tolerance)
	}
	if ppr.MaxNodes < 1 {
		return fmt.Errorf("ppr.max_nodes: must be at least 1, got %d", ppr.MaxNodes)
	}
	if err := checkNonNegative("ppr.edges", ppr.Edges); err != nil {
		return err
	}
	if w.Fusion.K < 0 {
		return errNegative("fusion.k", w.Fusion.K)
	}
	if err := checkNonNegative("fusion.lists", w.Fusion.Lists); err != nil {
		return err
	}
	if w.Context.Floor < 0 {
		return errNegative("context.floor", w.Context.Floor)
	}
	if w.Context.TaskLimit < 1 {
		return fmt.Errorf("context.task_limit: must be at least 1, got %d", w.Context.TaskLimit)
	}
	return nil
}

func checkNonNegative(path string, values map[string]float64) error {
	for _, key := range slices.Sorted(maps.Keys(values)) {
		if values[key] < 0 {
			return errNegative(keyPath(path, key), values[key])
		}
	}
	return nil
}

func errNegative(path string, value float64) error {
	return fmt.Errorf("%s: must not be negative, got %v", path, value)
}
