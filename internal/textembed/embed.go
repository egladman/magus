package textembed

import (
	"cmp"
	"fmt"
	"math"
	"math/bits"
	"slices"
	"strings"
)

// maxWeighted bounds Σ len(Tokens)·Weight over one Embed call, which keeps every sum
// below in int64: |v_j| ≤ 2^31·65535·127 < 2^54, and 254·|v_j| < 2^62.
const maxWeighted = 1 << 31

// Field is one field of a text: its tokens as textrank.Tokens produced them, and an
// integer multiplier on their counts. Weight 0 drops the field.
type Field struct {
	Tokens []string
	Weight int
}

// Vector is a text's embedding. Q is its direction, scaled so the largest component is
// ±127. InvNorm is ⌊2^31/‖Q‖⌋. Code holds Q's sign bits: bit j%64 of Code[j/64] is set
// when Q[j] > 0.
type Vector struct {
	Q       []int8
	InvNorm uint32
	Code    []uint64
}

// Contribution is one query token's share of a document's score, in score units.
type Contribution struct {
	Token string
	Score int64
}

// term is one distinct vocabulary token of a text and its weighted count.
type term struct {
	idx  int
	mult int64
}

// Embed sums count·Weight·w16·q8 over every token of fields the table holds, in int64,
// and quantizes the sum: Q[j] = round-half-away(127·v[j]/max|v|). Nothing in it is a
// float, so a Vector is bit-identical on every architecture. It reports false, with a
// zero Vector, when no token is in the vocabulary or the sum is zero; such a text is
// never ranked. A negative Weight, or fields weighing more than 2^31 tokens in all,
// panics.
func (t *Table) Embed(fields []Field) (Vector, bool) {
	v := t.sum(t.terms(fields))
	m := maxAbs(v)
	if m == 0 {
		return Vector{}, false
	}
	q := quantize(v, m)
	return Vector{Q: q, InvNorm: invNorm(q), Code: signCode(q)}, true
}

// Contributions splits d's score against q, int64(Σ q.Q[j]·d.Q[j])·d.InvNorm, across
// the distinct vocabulary tokens of fields: token t gets
// round-half-away(127·count·w16·Σ q8[j]·d.Q[j]·d.InvNorm / max|v|). The shares sum to
// the score up to the rounding Embed applied to q.Q, and saturate at the int64 range,
// which only tokens cancelling each other almost exactly can reach. They come highest
// first, ties by token. q must be t.Embed(fields)'s Vector and d a Vector of t.Dims();
// anything else panics.
func (t *Table) Contributions(q Vector, fields []Field, d Vector) []Contribution {
	terms := t.terms(fields)
	v := t.sum(terms)
	m := maxAbs(v)
	var want []int8
	if m > 0 {
		want = quantize(v, m)
	}
	if !slices.Equal(q.Q, want) {
		panic("textembed: Contributions: q is not the Vector of fields")
	}
	if m == 0 {
		return nil
	}
	if len(d.Q) != t.dims {
		panic(fmt.Sprintf("textembed: Contributions: d has %d dims, the table %d", len(d.Q), t.dims))
	}
	k := 127 * uint64(d.InvNorm)
	out := make([]Contribution, len(terms))
	for i, tm := range terms {
		var dot int32
		for j, b := range t.row(tm.idx) {
			dot += int32(int8(b)) * int32(d.Q[j])
		}
		out[i] = Contribution{
			Token: string(t.token(tm.idx)),
			Score: share(tm.mult*int64(t.weight(tm.idx)), dot, k, uint64(m)),
		}
	}
	slices.SortFunc(out, func(a, b Contribution) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return strings.Compare(a.Token, b.Token)
	})
	return out
}

// terms resolves fields to their distinct vocabulary tokens, sorted by index.
func (t *Table) terms(fields []Field) []term {
	var total int64
	var count int
	for _, f := range fields {
		if f.Weight < 0 || f.Weight > maxWeighted {
			panic(fmt.Sprintf("textembed: field weight %d is outside [0, %d]", f.Weight, maxWeighted))
		}
		total += int64(len(f.Tokens)) * int64(f.Weight)
		if total > maxWeighted {
			panic(fmt.Sprintf("textembed: fields weigh more than %d tokens", maxWeighted))
		}
		if f.Weight > 0 {
			count += len(f.Tokens)
		}
	}
	out := make([]term, 0, count)
	for _, f := range fields {
		if f.Weight == 0 {
			continue
		}
		for _, tok := range f.Tokens {
			if i, ok := t.lookup(tok); ok {
				out = append(out, term{idx: i, mult: int64(f.Weight)})
			}
		}
	}
	slices.SortFunc(out, func(a, b term) int { return cmp.Compare(a.idx, b.idx) })
	merged := out[:0]
	for _, tm := range out {
		if n := len(merged); n > 0 && merged[n-1].idx == tm.idx {
			merged[n-1].mult += tm.mult
			continue
		}
		merged = append(merged, tm)
	}
	return merged
}

func (t *Table) sum(terms []term) []int64 {
	v := make([]int64, t.dims)
	for _, tm := range terms {
		k := tm.mult * int64(t.weight(tm.idx))
		row := t.row(tm.idx)
		v := v[:len(row)]
		for j, b := range row {
			v[j] += k * int64(int8(b))
		}
	}
	return v
}

func maxAbs(v []int64) int64 {
	var m int64
	for _, x := range v {
		m = max(m, x, -x)
	}
	return m
}

func quantize(v []int64, m int64) []int8 {
	q := make([]int8, len(v))
	for j, x := range v {
		a := (254*max(x, -x) + m) / (2 * m)
		if x < 0 {
			a = -a
		}
		q[j] = int8(a)
	}
	return q
}

// invNorm is ⌊sqrt(⌊2^62/ΣQ²⌋)⌋, which equals ⌊2^31/‖Q‖⌋. ΣQ² ≥ 127², so it fits a
// uint32.
func invNorm(q []int8) uint32 {
	var s uint64
	for _, x := range q {
		s += uint64(int64(x) * int64(x))
	}
	return uint32(isqrt(1 << 62 / s))
}

// isqrt is ⌊√x⌋ by Newton's method from above.
func isqrt(x uint64) uint64 {
	if x < 2 {
		return x
	}
	r := uint64(1) << ((bits.Len64(x) + 1) / 2)
	for {
		next := (r + x/r) / 2
		if next >= r {
			return r
		}
		r = next
	}
}

func signCode(q []int8) []uint64 {
	code := make([]uint64, len(q)/64)
	for j, x := range q {
		if x > 0 {
			code[j/64] |= 1 << (j % 64)
		}
	}
	return code
}

// share is round-half-away(a·b·k/div) in 128-bit arithmetic, saturating at ±MaxInt64.
// Its callers keep |a| < 2^47, |b| < 2^31 and k < 2^38, so the product stays below
// 2^116.
func share(a int64, b int32, k, div uint64) int64 {
	neg := (a < 0) != (b < 0)
	hi, lo := bits.Mul64(absUint(a), absUint(int64(b)))
	carry, lo := bits.Mul64(lo, k)
	hi = hi*k + carry
	if hi >= div {
		return saturate(neg)
	}
	quo, rem := bits.Div64(hi, lo, div)
	if quo >= math.MaxInt64 {
		return saturate(neg)
	}
	if rem >= div-rem {
		quo++
	}
	if neg {
		return -int64(quo)
	}
	return int64(quo)
}

func absUint(x int64) uint64 {
	if x < 0 {
		return uint64(-x)
	}
	return uint64(x)
}

func saturate(neg bool) int64 {
	if neg {
		return -math.MaxInt64
	}
	return math.MaxInt64
}
