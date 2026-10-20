package distill

import (
	"errors"
	"iter"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type textsFixture []Text

func (c textsFixture) Texts() iter.Seq2[Text, error] {
	return func(yield func(Text, error) bool) {
		for _, t := range c {
			if !yield(t, nil) {
				return
			}
		}
	}
}

type pairsFixture []TextPair

func (p pairsFixture) Pairs() iter.Seq2[TextPair, error] {
	return func(yield func(TextPair, error) bool) {
		for _, tp := range p {
			if !yield(tp, nil) {
				return
			}
		}
	}
}

// failing yields one text and then err.
type failing struct{ err error }

func (f failing) Texts() iter.Seq2[Text, error] {
	return func(yield func(Text, error) bool) {
		if yield(Text{ID: "a", Body: "alpha"}, nil) {
			yield(Text{}, f.err)
		}
	}
}

func (f failing) Pairs() iter.Seq2[TextPair, error] {
	return func(yield func(TextPair, error) bool) { yield(TextPair{}, f.err) }
}

func fixtureVocab(t *testing.T) *Vocab {
	t.Helper()
	v, err := NewVocab([]string{"spell", "load", "engine"}, []int64{5, 3, 1}, 20)
	require.NoError(t, err)
	return v
}

func TestTextsExamplesSkipTextsWithNoVocabularyToken(t *testing.T) {
	v := fixtureVocab(t)
	c := textsFixture{
		{ID: "a", Body: "loadSpell spell"},
		{ID: "b", Body: "nothing known here"},
		{ID: "c", Body: "Engine"},
	}
	target := func(id string) ([]float64, bool) { return []float64{float64(len(id))}, id != "" }
	ex, empty, err := Examples(v, c, target)
	require.NoError(t, err)
	assert.Equal(t, 1, empty)
	require.Len(t, ex, 2)
	// loadSpell yields loadspell, load, spell; loadspell is not in the vocabulary.
	assert.Equal(t, Bag{Rows: []int32{0, 1}, Counts: []float64{2, 1}}, ex[0].Bag)
	assert.Equal(t, Bag{Rows: []int32{2}, Counts: []float64{1}}, ex[1].Bag)

	_, _, err = Examples(v, c, func(id string) ([]float64, bool) { return nil, id != "b" })
	assert.ErrorContains(t, err, `text "b" has no teacher target`)

	_, _, err = Examples(v, textsFixture{{ID: "z", Body: "unknown"}}, target)
	assert.ErrorContains(t, err, "no text holds")

	boom := errors.New("boom")
	_, _, err = Examples(v, failing{boom}, target)
	assert.ErrorIs(t, err, boom)
}

func TestTextsBagPairsSkipPairsThatTeachNothing(t *testing.T) {
	v := fixtureVocab(t)
	pairs, skipped, err := BagPairs(v, pairsFixture{
		{Source: "go-doc", Query: "load a spell", Doc: "func loadSpell(engine)"},
		{Source: "go-doc", Query: "unknown words", Doc: "spell"},
		{Source: "go-doc", Query: "spell load", Doc: "load spell"},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, skipped)
	require.Len(t, pairs, 1)
	assert.Equal(t, Bag{Rows: []int32{0, 1}, Counts: []float64{1, 1}}, pairs[0].Query)

	boom := errors.New("boom")
	_, _, err = BagPairs(v, failing{boom})
	assert.ErrorIs(t, err, boom)
}

func TestTextsIDHashDependsOnSaltAndID(t *testing.T) {
	assert.Equal(t, IDHash("s:", "x"), IDHash("s:", "x"))
	assert.NotEqual(t, IDHash("s:", "x"), IDHash("t:", "x"))
	assert.NotEqual(t, IDHash("s:", "x"), IDHash("s:", "y"))
	// sha256("s:x") begins b9cc050815a80145; pinning it catches a change of hash or
	// byte order.
	assert.Equal(t, uint64(0xb9cc050815a80145), IDHash("s:", "x"))
}
