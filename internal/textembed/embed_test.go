package textembed

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/egladman/magus/internal/textrank"
)

// handTable holds rows simple enough to embed by hand: a is 127 on axis 0 at weight 2,
// b is -1 on axis 1 and +1 on axis 2 at weight 1, and c and d cancel each other.
func handTable(t *testing.T) *Table {
	t.Helper()
	row := func(set map[int]int8) []int8 {
		r := make([]int8, 64)
		for j, x := range set {
			r[j] = x
		}
		return r
	}
	return read(t, encode(t, TableSpec{
		Tokens:  []string{"a", "b", "c", "d"},
		Weights: []uint16{2, 1, 5, 5},
		Rows: [][]int8{
			row(map[int]int8{0: 127}),
			row(map[int]int8{1: -1, 2: 1}),
			row(map[int]int8{3: 100, 4: -7}),
			row(map[int]int8{3: -100, 4: 7}),
		},
		Prov: testProvenance(),
	}))
}

func probeTable(t testing.TB) *Table {
	t.Helper()
	return read(t, encode(t, testSpec(probeVocab(), 256)))
}

// checkVector asserts what every Vector Embed returns must hold.
func checkVector(t *testing.T, v Vector, dims int) {
	t.Helper()
	if len(v.Q) != dims {
		t.Fatalf("len(Q) = %d, want %d", len(v.Q), dims)
	}
	var peak int
	var s uint64
	for _, x := range v.Q {
		peak = max(peak, int(x), -int(x))
		s += uint64(int(x) * int(x))
	}
	if peak != 127 {
		t.Errorf("largest |Q| = %d, want 127", peak)
	}
	r := uint64(v.InvNorm)
	if want := uint64(1<<62) / s; r*r > want || (r+1)*(r+1) <= want {
		t.Errorf("InvNorm = %d is not ⌊√(2^62/%d)⌋", v.InvNorm, s)
	}
}

func TestEmbedByHand(t *testing.T) {
	tbl := handTable(t)
	tests := []struct {
		name   string
		fields []Field
		q      map[int]int8
	}{
		{
			// v = (254, -1, 1): 127·254/254 = 127 and ±127/254 = ±0.5 round away from zero.
			name:   "halves round away from zero",
			fields: []Field{{Tokens: []string{"a", "b", "absent"}, Weight: 1}},
			q:      map[int]int8{0: 127, 1: -1, 2: 1},
		},
		{
			// a counts 3+1, so v = (1016, -1, 1), and 127/1016 = 0.125 rounds to 0.
			name:   "weights multiply counts",
			fields: []Field{{Tokens: []string{"a"}, Weight: 3}, {Tokens: []string{"a", "b"}, Weight: 1}},
			q:      map[int]int8{0: 127},
		},
		{
			name:   "cancelled tokens leave the rest",
			fields: []Field{{Tokens: []string{"c", "b", "d"}, Weight: 1}},
			q:      map[int]int8{1: -127, 2: 127},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, ok := tbl.Embed(tt.fields)
			if !ok {
				t.Fatal("Embed reported no vector")
			}
			checkVector(t, v, 64)
			want := make([]int8, 64)
			for j, x := range tt.q {
				want[j] = x
			}
			if !slices.Equal(v.Q, want) {
				t.Errorf("Q = %v, want %v", v.Q, want)
			}
		})
	}
}

func TestEmbedNoVector(t *testing.T) {
	tbl := handTable(t)
	for name, fields := range map[string][]Field{
		"no fields":     nil,
		"no tokens":     {{Weight: 1}},
		"no vocabulary": {{Tokens: []string{"absent", "zzz"}, Weight: 1}},
		"weight zero":   {{Tokens: []string{"a", "b"}, Weight: 0}},
		"sum cancels":   {{Tokens: []string{"c", "d"}, Weight: 1}},
		"fields cancel": {{Tokens: []string{"c"}, Weight: 2}, {Tokens: []string{"d", "d"}, Weight: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			v, ok := tbl.Embed(fields)
			if ok || !reflect.DeepEqual(v, Vector{}) {
				t.Errorf("Embed = %+v, %v; want a zero Vector, false", v, ok)
			}
			if got := tbl.Contributions(v, fields, Vector{}); got != nil {
				t.Errorf("Contributions = %v, want nil", got)
			}
		})
	}
}

func TestEmbedPanics(t *testing.T) {
	tbl := handTable(t)
	for name, fields := range map[string][]Field{
		"negative weight": {{Tokens: []string{"a"}, Weight: -1}},
		"weight over cap": {{Tokens: []string{"a"}, Weight: maxWeighted + 1}},
		"total over cap":  {{Tokens: []string{"a"}, Weight: maxWeighted}, {Tokens: []string{"absent"}, Weight: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Error("Embed did not panic")
				}
			}()
			tbl.Embed(fields)
		})
	}
}

// TestEmbedOrderFree checks the integer sums make a Vector independent of token order,
// field order, and how counts are split across fields of one weight.
func TestEmbedOrderFree(t *testing.T) {
	tbl := probeTable(t)
	name := textrank.Tokens("parseJSONBody HTTPServer")
	doc := textrank.Tokens(probe)
	want, ok := tbl.Embed([]Field{{Tokens: name, Weight: 2}, {Tokens: doc, Weight: 1}})
	if !ok {
		t.Fatal("Embed reported no vector")
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 20 {
		shuffled := slices.Clone(doc)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		cut := rng.IntN(len(shuffled))
		fields := []Field{
			{Tokens: shuffled[:cut], Weight: 1},
			{Tokens: name, Weight: 1},
			{Tokens: shuffled[cut:], Weight: 1},
			{Tokens: name, Weight: 1},
		}
		rng.Shuffle(len(fields), func(a, b int) { fields[a], fields[b] = fields[b], fields[a] })
		got, _ := tbl.Embed(fields)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("permutation %d embeds differently", i)
		}
	}
}

func vectorDigest(v Vector) string {
	h := sha256.New()
	for _, x := range v.Q {
		h.Write([]byte{byte(x)})
	}
	h.Write(binary.LittleEndian.AppendUint32(nil, v.InvNorm))
	return hex.EncodeToString(h.Sum(nil))
}

// TestEmbedGolden pins Vectors over the probe table. Embed uses no float, so these
// hold byte for byte on amd64 and arm64; a mismatch on one architecture only is a
// determinism bug, never a reason to update want.
func TestEmbedGolden(t *testing.T) {
	tbl := probeTable(t)
	tests := []struct {
		name    string
		fields  []Field
		invNorm uint32
		digest  string
	}{
		{"prose", []Field{{Tokens: textrank.Tokens("How does the Buzz engine load a spell"), Weight: 1}}, 2674941, "880a42223fea62ab0f9d3532d11a6ffce7f6cf7dc009c1d8202e9766237175b8"},
		{"identifiers", []Field{{Tokens: textrank.Tokens("HTTPServer TLSConfig fs\\readFile"), Weight: 1}}, 3579542, "3c1da3f471642a3a60e154a22d35e88d58cd643ceeacccc04addfd17037f8df8"},
		{"weighted fields", []Field{
			{Tokens: textrank.Tokens("WriteTo"), Weight: 2},
			{Tokens: textrank.Tokens("func (ix *Index) WriteTo(w io.Writer) (int64, error)"), Weight: 1},
			{Tokens: textrank.Tokens("internal/textrank/bm25f.go"), Weight: 1},
		}, 2603591, "37dbbebf67837d0803a737858c0277da0443dc6337d8205e1afb32eba5d312d6"},
		{"whole probe", []Field{{Tokens: textrank.Tokens(probe), Weight: 1}}, 3622872, "f18190939b26b1a400791d5fb8451e2e1162a453e68ed01d937acc563a44f5d4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, ok := tbl.Embed(tt.fields)
			if !ok {
				t.Fatal("Embed reported no vector")
			}
			checkVector(t, v, 256)
			if got := vectorDigest(v); v.InvNorm != tt.invNorm || got != tt.digest {
				t.Errorf("InvNorm, digest = %d, %q; want %d, %q", v.InvNorm, got, tt.invNorm, tt.digest)
			}
		})
	}
}

func score(q, d Vector) int64 {
	var dot int32
	for j := range q.Q {
		dot += int32(q.Q[j]) * int32(d.Q[j])
	}
	return int64(dot) * int64(d.InvNorm)
}

func TestContributions(t *testing.T) {
	tbl := probeTable(t)
	fields := []Field{
		{Tokens: textrank.Tokens("load spell engine Buzz spell"), Weight: 2},
		{Tokens: textrank.Tokens("graph build zzqx_unseen"), Weight: 1},
	}
	q, _ := tbl.Embed(fields)
	d, _ := tbl.Embed([]Field{{Tokens: textrank.Tokens(probe), Weight: 1}})
	got := tbl.Contributions(q, fields, d)

	var tokens []string
	var sum int64
	for _, c := range got {
		tokens = append(tokens, c.Token)
		sum += c.Score
	}
	if want := []string{"build", "buzz", "engine", "graph", "load", "spell"}; !slices.Equal(slices.Sorted(slices.Values(tokens)), want) {
		t.Errorf("tokens = %v, want each of %v once", tokens, want)
	}
	for i := 1; i < len(got); i++ {
		if a, b := got[i-1], got[i]; a.Score < b.Score || a.Score == b.Score && a.Token > b.Token {
			t.Fatalf("Contributions out of order at %d: %v", i, got)
		}
	}
	// Each Q[j] is off its exact value by at most 1/2, and each share by 1/2.
	var l1 int64
	for _, x := range d.Q {
		l1 += int64(max(x, -x))
	}
	if diff, bound := sum-score(q, d), int64(d.InvNorm)*l1/2+int64(len(got)); max(diff, -diff) > bound {
		t.Errorf("shares sum to %d, the score is %d: off by more than %d", sum, score(q, d), bound)
	}
	const want = "[{buzz 100782930440} {build 100325195900} {graph 25371472693}]"
	if top := fmt.Sprint(got[:3]); top != want {
		t.Errorf("top shares = %s, want %s", top, want)
	}
}

func TestContributionsPanics(t *testing.T) {
	tbl := handTable(t)
	fields := []Field{{Tokens: []string{"a", "b"}, Weight: 1}}
	q, _ := tbl.Embed(fields)
	other, _ := tbl.Embed([]Field{{Tokens: []string{"b"}, Weight: 1}})
	for name, call := range map[string]func(){
		"q of other fields": func() { tbl.Contributions(other, fields, q) },
		"q of no fields":    func() { tbl.Contributions(Vector{}, fields, q) },
		"d of other dims":   func() { tbl.Contributions(q, fields, Vector{Q: make([]int8, 128)}) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Error("Contributions did not panic")
				}
			}()
			call()
		})
	}
}

func TestContributionsShare(t *testing.T) {
	tests := []struct {
		a      int64
		b      int32
		k, div uint64
		want   int64
	}{
		{1, 1, 3, 2, 2},
		{-1, 1, 3, 2, -2},
		{1, -1, 3, 2, -2},
		{-1, -1, 3, 2, 2},
		{1, 1, 5, 4, 1},
		{0, 1, 1, 1, 0},
		{1 << 46, math.MaxInt32, 1 << 37, 1, math.MaxInt64},
		{-(1 << 46), math.MaxInt32, 1 << 37, 1, -math.MaxInt64},
		{1 << 46, 1 << 30, 1 << 37, 1 << 50, math.MaxInt64},
		{1 << 46, 1 << 30, 1 << 37, 1 << 51, 1 << 62},
	}
	for _, tt := range tests {
		if got := share(tt.a, tt.b, tt.k, tt.div); got != tt.want {
			t.Errorf("share(%d, %d, %d, %d) = %d, want %d", tt.a, tt.b, tt.k, tt.div, got, tt.want)
		}
	}

	rng := rand.New(rand.NewPCG(3, 4))
	for range 10000 {
		a := rng.Int64N(1<<47) - 1<<46
		b := int32(rng.Int64N(1<<31) - 1<<30)
		k := rng.Uint64N(1 << 38)
		div := rng.Uint64N(1<<60) + 1
		if got, want := share(a, b, k, div), shareBig(a, b, k, div); got != want {
			t.Fatalf("share(%d, %d, %d, %d) = %d, math/big says %d", a, b, k, div, got, want)
		}
	}
}

func shareBig(a int64, b int32, k, div uint64) int64 {
	num := new(big.Int).Mul(big.NewInt(a), big.NewInt(int64(b)))
	num.Mul(num, new(big.Int).SetUint64(k))
	neg := num.Sign() < 0
	num.Abs(num)
	d := new(big.Int).SetUint64(div)
	quo, rem := new(big.Int).QuoRem(num, d, new(big.Int))
	if rem.Lsh(rem, 1).Cmp(d) >= 0 {
		quo.Add(quo, big.NewInt(1))
	}
	if !quo.IsInt64() || quo.Int64() == math.MaxInt64 {
		return saturate(neg)
	}
	if neg {
		return -quo.Int64()
	}
	return quo.Int64()
}

func TestEmbedIsqrt(t *testing.T) {
	check := func(x uint64) {
		r := isqrt(x)
		hi, lo := bits.Mul64(r, r)
		hi1, lo1 := bits.Mul64(r+1, r+1)
		if hi > 0 || lo > x || (hi1 == 0 && lo1 <= x) {
			t.Fatalf("isqrt(%d) = %d", x, r)
		}
	}
	for x := range uint64(1 << 20) {
		check(x)
	}
	for _, x := range []uint64{1<<62 - 1, 1 << 62, 1<<64 - 1, (1<<32 - 1) * (1<<32 - 1), (1<<32-1)*(1<<32-1) - 1} {
		check(x)
	}
	rng := rand.New(rand.NewPCG(5, 6))
	for range 100000 {
		check(rng.Uint64())
	}
}

// benchWords is n distinct random lowercase words of 3 to 10 letters, so their
// prefixes spread the way a real vocabulary's do.
func benchWords(n int) []string {
	rng := rand.New(rand.NewPCG(9, 10))
	seen := make(map[string]bool, n)
	out := make([]string, 0, n)
	for len(out) < n {
		w := make([]byte, 3+rng.IntN(8))
		for i := range w {
			w[i] = byte('a' + rng.IntN(26))
		}
		if !seen[string(w)] {
			seen[string(w)] = true
			out = append(out, string(w))
		}
	}
	return out
}

// benchTable is the shipped budget's shape: 29k tokens of 256 dims. It returns, beside
// the table, a fifth as many words it lacks.
func benchTable(b *testing.B) (tbl *Table, data []byte, words []string) {
	b.Helper()
	words = benchWords(29000 + 29000/5)
	vocab := slices.Sorted(slices.Values(words[:29000]))
	data = encode(b, testSpec(vocab, 256))
	return read(b, data), data, words
}

func BenchmarkEmbed(b *testing.B) {
	tbl, _, all := benchTable(b)
	rng := rand.New(rand.NewPCG(7, 8))
	words := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = all[rng.IntN(len(all))]
		}
		return out
	}
	for _, bc := range []struct {
		name   string
		fields []Field
	}{
		{"query8", []Field{{Tokens: words(8), Weight: 1}}},
		{"doc400", []Field{
			{Tokens: words(4), Weight: 2},
			{Tokens: words(16), Weight: 1},
			{Tokens: words(380), Weight: 1},
		}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				tbl.Embed(bc.fields)
			}
		})
		// The vocabulary lookups alone, the share of Embed no arithmetic speedup reaches.
		b.Run(bc.name+"/terms", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				tbl.terms(bc.fields)
			}
		})
	}
}

func BenchmarkReadTable(b *testing.B) {
	_, data, _ := benchTable(b)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ReadTable(data); err != nil {
			b.Fatal(err)
		}
	}
}
