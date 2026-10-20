package distill

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/textembed"
)

// Quantize turns a trained model into the table codec's spec. Token t stores
// r_t = ω_t·e_t as q_t = round(127·r_t/max|r_t|) and w16_t = round(65535·c_t/max c)
// with c_t = max|r_t|, so w16_t·q_t is r_t up to one scale shared by every token, which
// the runtime's cosine ignores. tokens name the model's rows in order; the spec sorts
// them, as the codec requires.
func Quantize(m *Model, tokens []string, prov textembed.Provenance) (textembed.TableSpec, error) {
	if len(tokens) != m.Len() {
		return textembed.TableSpec{}, fmt.Errorf("quantize: %d tokens for a model of %d rows", len(tokens), m.Len())
	}
	r := make([]float64, m.dims)
	scale := make([]float64, m.Len())
	rows := make([][]int8, m.Len())
	var top float64
	for t := range m.Len() {
		w := m.Weight(t)
		var c float64
		for j, e := range m.Direction(t) {
			r[j] = w * e
			c = max(c, math.Abs(r[j]))
		}
		if math.IsNaN(c) || math.IsInf(c, 0) {
			return textembed.TableSpec{}, fmt.Errorf("quantize: token %q is not finite", tokens[t])
		}
		q := make([]int8, m.dims)
		if c > 0 {
			for j, x := range r {
				q[j] = int8(math.Round(127 * x / c))
			}
		}
		rows[t], scale[t] = q, c
		top = max(top, c)
	}
	if top == 0 {
		return textembed.TableSpec{}, fmt.Errorf("quantize: every token is zero")
	}

	order := make([]int, m.Len())
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return strings.Compare(tokens[a], tokens[b]) })
	spec := textembed.TableSpec{
		Tokens:  make([]string, len(order)),
		Weights: make([]uint16, len(order)),
		Rows:    make([][]int8, len(order)),
		Prov:    prov,
	}
	for i, t := range order {
		spec.Tokens[i] = tokens[t]
		spec.Weights[i] = uint16(math.Round(65535 * scale[t] / top))
		spec.Rows[i] = rows[t]
	}
	return spec, nil
}

// WriteTable quantizes m over v's tokens and encodes it with textembed.EncodeTable,
// which validates the spec and writes nothing when it is invalid.
func WriteTable(w io.Writer, m *Model, v *Vocab, prov textembed.Provenance) error {
	spec, err := Quantize(m, v.Tokens, prov)
	if err != nil {
		return err
	}
	return textembed.EncodeTable(w, spec)
}
