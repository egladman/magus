package proofread

import (
	"cmp"
	"encoding/json"
	"math"
	"slices"
)

// minSeparationTexts is the fewest texts on either side that [Separate]
// scores a feature from.
const minSeparationTexts = 5

// Separation is how far one [Shape] feature separates two sets of texts.
type Separation struct {
	// Feature is the feature's JSON path in [Shape]: a number by its name, a
	// map entry as "<map>.<key>".
	Feature string `json:"feature"`
	// AUC is the probability that a random owner text measures higher than a
	// random other text, ties counted half: 1 or 0 separates the sets
	// completely, 0.5 not at all.
	AUC float64 `json:"auc"`
	// Owner and Other are the texts on each side the feature was measured on.
	Owner int `json:"owner"`
	Other int `json:"other"`
}

// Separate scores every feature of [Shape] by how well it tells the owner's
// texts from the other texts, most separating first, then by name.
//
// The features come from Shape's JSON encoding, so a field added to Shape is
// scored without a change here; words, sentences and withheld are not
// features. A text counts toward a feature unless its Withheld names the
// feature or the map holding it, and a feature a counted text lacks, such as
// an entry its map omits, reads 0. A shape holding a NaN or an infinity
// counts toward nothing. A feature measured on fewer than 5 texts on
// either side is left out. The result is empty, never nil, when no feature is
// scored.
func Separate(owner, other []Shape) []Separation {
	names := map[string]string{}
	ownerTexts, otherTexts := flattenShapes(owner, names), flattenShapes(other, names)
	out := []Separation{}

	for name, top := range names {
		ov, xv := featureValues(ownerTexts, name, top), featureValues(otherTexts, name, top)
		if len(ov) < minSeparationTexts || len(xv) < minSeparationTexts {
			continue
		}

		out = append(out, Separation{Feature: name, AUC: auc(ov, xv), Owner: len(ov), Other: len(xv)})
	}

	slices.SortFunc(out, func(a, b Separation) int {
		return cmp.Or(cmp.Compare(math.Abs(b.AUC-0.5), math.Abs(a.AUC-0.5)), cmp.Compare(a.Feature, b.Feature))
	})

	return out
}

// flatShape is one text's features by path and the names it withholds.
type flatShape struct {
	values   map[string]float64
	withheld map[string]bool
}

// flattenShapes flattens each shape that encodes, and records every feature
// path it finds in names, mapped to the top-level field holding it.
func flattenShapes(shapes []Shape, names map[string]string) []flatShape {
	out := make([]flatShape, 0, len(shapes))

	for _, s := range shapes {
		// A NaN or an infinity cannot be encoded, and no rank holds it.
		data, err := json.Marshal(s)
		if err != nil {
			continue
		}

		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			continue
		}

		f := flatShape{values: map[string]float64{}, withheld: map[string]bool{}}

		for _, w := range s.Withheld {
			f.withheld[w] = true
		}

		for key, v := range fields {
			switch key {
			case "words", "sentences", "withheld":
				continue
			}

			flatten(key, key, v, f.values, names)
		}

		out = append(out, f)
	}

	return out
}

// featureValues returns the feature's value on each text that counts toward
// it.
func featureValues(texts []flatShape, name, top string) []float64 {
	out := make([]float64, 0, len(texts))

	for _, f := range texts {
		if !f.withheld[top] && !f.withheld[name] {
			out = append(out, f.values[name])
		}
	}

	return out
}

// flatten adds the numbers v holds to values under their path, and records
// each path's top-level field in names.
func flatten(top, path string, v any, values map[string]float64, names map[string]string) {
	switch v := v.(type) {
	case float64:
		values[path] = v
		names[path] = top
	case map[string]any:
		for key, inner := range v {
			flatten(top, path+"."+key, inner, values, names)
		}
	}
}

// auc is the Mann-Whitney U of owner over other divided by the pairs, from
// the ranks of the pooled values with tied values sharing their mean rank.
func auc(owner, other []float64) float64 {
	type value struct {
		v     float64
		owner bool
	}

	pooled := make([]value, 0, len(owner)+len(other))
	for _, v := range owner {
		pooled = append(pooled, value{v, true})
	}

	for _, v := range other {
		pooled = append(pooled, value{v, false})
	}

	slices.SortFunc(pooled, func(a, b value) int { return cmp.Compare(a.v, b.v) })

	var ownerRanks float64

	for i := 0; i < len(pooled); {
		j := i
		for j < len(pooled) && pooled[j].v == pooled[i].v {
			j++
		}

		rank := float64(i+j+1) / 2

		for _, p := range pooled[i:j] {
			if p.owner {
				ownerRanks += rank
			}
		}

		i = j
	}

	n1, n2 := float64(len(owner)), float64(len(other))

	return (ownerRanks - n1*(n1+1)/2) / (n1 * n2)
}
