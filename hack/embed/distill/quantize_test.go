package distill

import (
	"bytes"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/egladman/magus/internal/textembed"
	"github.com/egladman/magus/internal/textrank"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testProvenance() textembed.Provenance {
	return textembed.Provenance{
		Teacher: "toy", TeacherRevision: "r1", CorpusDigest: "c", PairsDigest: "p",
		Pipeline: "test", Run: "run-1", GOARCH: "test", Seed: 7,
	}
}

// wordVocab is n tokens w000..w(n-1) with descending counts.
func wordVocab(t *testing.T, n int) *Vocab {
	t.Helper()
	toks := make([]string, n)
	counts := make([]int64, n)
	for i := range n {
		toks[i], counts[i] = fmt.Sprintf("w%03d", n-1-i), int64(n-i)
	}
	v, err := NewVocab(toks, counts, int64(n*n))
	require.NoError(t, err)
	return v
}

// cosine8 is the cosine between an int8 runtime vector and a float one.
func cosine8(q []int8, s []float64) float64 {
	var d, nq, ns float64
	for j, x := range q {
		d += float64(x) * s[j]
		nq += float64(x) * float64(x)
		ns += s[j] * s[j]
	}
	return d / math.Sqrt(nq*ns)
}

func TestQuantizeRoundTripsThroughReadTable(t *testing.T) {
	rng := rand.New(rand.NewPCG(31, 0))
	v := wordVocab(t, 50)
	m := randomModel(t, rng, v.Len(), 64)

	var buf bytes.Buffer
	require.NoError(t, WriteTable(&buf, m, v, testProvenance()))
	tbl, err := textembed.ReadTable(buf.Bytes())
	require.NoError(t, err)
	assert.Equal(t, 64, tbl.Dims())
	assert.Equal(t, 50, tbl.Len())
	assert.Equal(t, "run-1", tbl.Provenance().Run)

	var again bytes.Buffer
	require.NoError(t, WriteTable(&again, m, v, testProvenance()))
	assert.Equal(t, buf.Bytes(), again.Bytes(), "one model encoded twice differs")

	var worst float64 = 1
	for range 40 {
		var words []string
		for range 1 + rng.IntN(8) {
			words = append(words, v.Tokens[rng.IntN(v.Len())])
		}
		text := fmt.Sprint(words)
		q, ok := tbl.Embed([]textembed.Field{{Tokens: textrank.Tokens(text), Weight: 1}})
		require.True(t, ok)
		worst = min(worst, cosine8(q.Q, m.Embed(v.Bag(text))))
	}
	t.Logf("worst runtime-vs-student cosine %.5f", worst)
	assert.GreaterOrEqual(t, worst, 0.995)
}

func TestQuantizeScalesEachRowToItsLargestEntry(t *testing.T) {
	m, err := NewModel([][]float64{{1, -2, 0, 0}, {4, 1, 0, 0}, {0, 0, 0, 1}}, []float64{1, 1, 0.5})
	require.NoError(t, err)
	// Four dims are below the codec's minimum; Quantize leaves that check to EncodeTable.
	spec, err := Quantize(m, []string{"b", "c", "a"}, testProvenance())
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, spec.Tokens)
	assert.Equal(t, []int8{0, 0, 0, 127}, spec.Rows[0])
	assert.Equal(t, []int8{64, -127, 0, 0}, spec.Rows[1]) // 127·1/2 = 63.5 rounds away
	assert.Equal(t, []int8{127, 32, 0, 0}, spec.Rows[2])

	// c_t = ω_t·max|e_t|: b and c both have ω = 1 and the largest unit entry 2/√5 and
	// 4/√17; a has ω = 0.5 and 1.
	cb, cc, ca := 2/math.Sqrt(5), 4/math.Sqrt(17), 0.5
	top := max(ca, cb, cc)
	want := []uint16{uint16(math.Round(65535 * ca / top)), uint16(math.Round(65535 * cb / top)), 65535}
	assert.Equal(t, want, spec.Weights)
	assert.True(t, slices.IsSorted(spec.Tokens))
}

func TestQuantizeRefuses(t *testing.T) {
	rng := rand.New(rand.NewPCG(32, 0))
	m := randomModel(t, rng, 3, 64)
	_, err := Quantize(m, []string{"a"}, testProvenance())
	assert.ErrorContains(t, err, "1 tokens for a model of 3 rows")

	// EncodeTable refuses what the runtime tokenizer would never yield.
	v := &Vocab{Tokens: []string{"a", "B", "c"}}
	err = WriteTable(&bytes.Buffer{}, m, v, testProvenance())
	assert.ErrorContains(t, err, "not what textrank.Tokens makes")
}
