package distill

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func vocabTexts() textsFixture {
	return textsFixture{
		{ID: "1", Body: "spell spell spell load load engine 2026 2026"},
		{ID: "2", Body: "spell load " + strings.Repeat("x", 33) + " " + strings.Repeat("x", 33)},
		{ID: "3", Body: "engine once"},
	}
}

func vocabOptions() VocabOptions {
	return VocabOptions{MinCount: 2, MaxBytes: 32, Size: 10}
}

func TestVocabKeepsFrequentTokensInRankOrder(t *testing.T) {
	v, err := BuildVocab(vocabTexts(), vocabOptions())
	require.NoError(t, err)
	// 2026 is all digits and the 33-byte run is too long; once is counted once.
	assert.Equal(t, []string{"spell", "load", "engine"}, v.Tokens)
	assert.Equal(t, []int64{4, 3, 2}, v.Counts)
	assert.Equal(t, int64(14), v.Total)
	assert.Equal(t, Bag{Rows: []int32{1}, Counts: []float64{1}}, v.Bag("load once"))
}

func TestVocabForcesTokensWhateverTheirCount(t *testing.T) {
	opts := vocabOptions()
	opts.Size = 6
	opts.Forced = []string{"fs\\readFile", "BZZ1001"}
	v, err := BuildVocab(vocabTexts(), opts)
	require.NoError(t, err)
	// Five forced tokens (fs, readfile, read, file, bzz1001) leave one slot, which goes
	// to the top-ranked counted token; the unseen forced ones rank last, by token.
	assert.Equal(t, []string{"spell", "bzz1001", "file", "fs", "read", "readfile"}, v.Tokens)
	assert.Equal(t, []int64{4, 0, 0, 0, 0, 0}, v.Counts)
}

func TestVocabRefuses(t *testing.T) {
	opts := vocabOptions()
	opts.Size = 2
	opts.Forced = []string{"alpha beta gamma"}
	_, err := BuildVocab(vocabTexts(), opts)
	assert.ErrorContains(t, err, "3 forced tokens exceed the size 2")

	_, err = BuildVocab(vocabTexts(), VocabOptions{})
	assert.ErrorContains(t, err, "must all be at least 1")

	opts = vocabOptions()
	opts.Forced = []string{"--"}
	_, err = BuildVocab(vocabTexts(), opts)
	assert.ErrorContains(t, err, "yields no token")

	for _, tc := range []struct {
		tokens []string
		counts []int64
		want   string
	}{
		{[]string{"Spell"}, []int64{1}, "not what textrank.Tokens"},
		{[]string{"a", "a"}, []int64{1, 1}, "repeats"},
		{[]string{"a", "b"}, []int64{1, 2}, "rank order"},
		{[]string{"b", "a"}, []int64{1, 1}, "rank order"},
		{[]string{"a"}, []int64{9}, "more than the total"},
		{nil, nil, "no tokens"},
	} {
		_, err := NewVocab(tc.tokens, tc.counts, 5)
		assert.ErrorContains(t, err, tc.want, "%v", tc.tokens)
	}
}

func TestVocabBagCountsWhatTheRuntimeTokenizerYields(t *testing.T) {
	v := fixtureVocab(t)
	b := v.Bag("LoadSpell(engine) spell")
	assert.Equal(t, Bag{Rows: []int32{0, 1, 2}, Counts: []float64{2, 1, 1}}, b)
	assert.Empty(t, v.Bag("nothing").Rows)
}
