package textrank

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixtureDocs() []Document {
	return []Document{
		{ID: "a", Fields: map[string][]string{FieldName: {"leaf", "score"}, FieldBody: {"leaf", "leaf", "x"}}},
		{ID: "b", Fields: map[string][]string{FieldName: {"score"}, FieldBody: {"leaf"}}},
		{ID: "c", Fields: map[string][]string{FieldName: {"other"}, FieldBody: {"x", "y"}, FieldPath: {"leaf"}}},
	}
}

// fixtureWeights leaves FieldPath unweighted, so c's path hit on "leaf" must not score.
func fixtureWeights() BM25F {
	return BM25F{K1: 1.2, Fields: map[string]FieldWeight{
		FieldName: {Weight: 5, B: 0.75},
		FieldBody: {Weight: 1, B: 0.75},
	}}
}

// The fixture's field lengths: name a=2 b=1 c=1 (avg 4/3), body a=3 b=1 c=2 (avg 2).
// "leaf" and "score" are each in 2 of 3 documents' weighted fields; c's path field
// holds "leaf" too, so df("leaf") is 3.
func TestIndexScoreHandComputed(t *testing.T) {
	ix := NewIndex(fixtureDocs())
	require.Equal(t, 3, ix.Len())

	idfLeaf := math.Log(1 + (3-3+0.5)/(3+0.5))
	idfScore := math.Log(1 + (3-2+0.5)/(2+0.5))
	// Length norms: name a 0.25+0.75*2/(4/3)=1.375, name b 0.25+0.75*1/(4/3)=0.8125,
	// body a 0.25+0.75*3/2=1.375, body b 0.25+0.75*1/2=0.625.
	leafA := 5*1/1.375 + 1*2/1.375
	leafB := 1 * 1 / 0.625
	scoreA := 5 * 1 / 1.375
	scoreB := 5 * 1 / 0.8125
	sat := func(tfw float64) float64 { return tfw / (1.2 + tfw) }

	hits := ix.Score([]string{"leaf", "score"}, fixtureWeights())
	require.Len(t, hits, 2, "c holds leaf only in an unweighted field")

	wantA := idfLeaf*sat(leafA) + idfScore*sat(scoreA)
	wantB := idfLeaf*sat(leafB) + idfScore*sat(scoreB)
	require.Greater(t, wantB, wantA, "the fixture is built so b outranks a")
	assert.Equal(t, "b", hits[0].ID)
	assert.InDelta(t, wantB, hits[0].Score, 1e-12)
	assert.Equal(t, 2, hits[0].Matched)
	assert.Equal(t, "a", hits[1].ID)
	assert.InDelta(t, wantA, hits[1].Score, 1e-12)

	assert.Equal(t, []string{"leaf/body", "leaf/name", "score/name"}, termFields(hits[1]))
	var sum float64
	for _, ts := range hits[1].Terms {
		sum += ts.Score
	}
	assert.InDelta(t, hits[1].Score, sum, 1e-12, "a hit's term scores add up to its score")
	assert.InDelta(t, idfLeaf*sat(leafA)*(2/1.375)/leafA, hits[1].Terms[0].Score, 1e-12)
}

func termFields(h Hit) []string {
	var out []string
	for _, ts := range h.Terms {
		out = append(out, ts.Term+"/"+ts.Field)
	}
	return out
}

// TestIndexScoreSaturatesPerDocument pins BM25F against the per-field BM25 sum it is
// not: a's "leaf" in name and body saturates once, over the weighted sum.
func TestIndexScoreSaturatesPerDocument(t *testing.T) {
	ix := NewIndex(fixtureDocs())
	idf := math.Log(1 + (3-3+0.5)/(3+0.5))
	name, body := 5*1/1.375, 1*2/1.375

	hits := ix.Score([]string{"leaf"}, fixtureWeights())
	require.Equal(t, "a", hits[0].ID)
	assert.InDelta(t, idf*(name+body)/(1.2+name+body), hits[0].Score, 1e-12)
	perField := idf*name/(1.2+name) + idf*body/(1.2+body)
	assert.Less(t, hits[0].Score, perField)
}

func TestIndexScoreRanksAllTermMatchesFirst(t *testing.T) {
	ix := NewIndex([]Document{
		{ID: "many", Fields: map[string][]string{FieldName: {"leaf", "leaf", "leaf"}}},
		{ID: "both", Fields: map[string][]string{FieldBody: {"leaf", "lower", "pad", "pad", "pad"}}},
	})
	hits := ix.Score([]string{"leaf", "lower", "leaf"}, fixtureWeights())
	require.Len(t, hits, 2)
	assert.Equal(t, "both", hits[0].ID)
	assert.Equal(t, 2, hits[0].Matched)
	assert.Equal(t, 1, hits[1].Matched, "a repeated query term counts once")
}

func TestIndexScoreIgnoresInputOrder(t *testing.T) {
	docs := fixtureDocs()
	reversed := []Document{docs[2], docs[1], docs[0]}
	q := []string{"leaf", "score", "x"}

	assert.Equal(t, NewIndex(docs).Score(q, fixtureWeights()), NewIndex(reversed).Score(q, fixtureWeights()))
}

func TestIndexScoreBreaksTiesByID(t *testing.T) {
	ix := NewIndex([]Document{
		{ID: "z", Fields: map[string][]string{FieldName: {"leaf"}}},
		{ID: "m", Fields: map[string][]string{FieldName: {"leaf"}}},
		{ID: "a", Fields: map[string][]string{FieldName: {"leaf"}}},
	})
	hits := ix.Score([]string{"leaf"}, fixtureWeights())
	require.Len(t, hits, 3)
	assert.Equal(t, []string{"a", "m", "z"}, []string{hits[0].ID, hits[1].ID, hits[2].ID})
}

func TestIndexScoreMergesDuplicateIDs(t *testing.T) {
	split := NewIndex([]Document{
		{ID: "a", Fields: map[string][]string{FieldName: {"leaf"}}},
		{ID: "b", Fields: map[string][]string{FieldBody: {"x"}}},
		{ID: "a", Fields: map[string][]string{FieldBody: {"leaf"}}},
	})
	whole := NewIndex([]Document{
		{ID: "a", Fields: map[string][]string{FieldName: {"leaf"}, FieldBody: {"leaf"}}},
		{ID: "b", Fields: map[string][]string{FieldBody: {"x"}}},
	})
	assert.Equal(t, 2, split.Len())
	assert.Equal(t, whole.Score([]string{"leaf"}, fixtureWeights()), split.Score([]string{"leaf"}, fixtureWeights()))
}

func TestIndexScoreEmpty(t *testing.T) {
	assert.Empty(t, NewIndex(nil).Score([]string{"leaf"}, fixtureWeights()))
	assert.Empty(t, NewIndex(fixtureDocs()).Score([]string{"absent"}, fixtureWeights()))
}

func TestIndexRoundTrip(t *testing.T) {
	ix := NewIndex(fixtureDocs())
	var buf bytes.Buffer
	n, err := ix.WriteTo(&buf)
	require.NoError(t, err)
	assert.Equal(t, int64(buf.Len()), n)
	encoded := bytes.Clone(buf.Bytes())

	back, err := ReadIndex(&buf)
	require.NoError(t, err)
	q := []string{"leaf", "score", "x", "y"}
	assert.Equal(t, ix.Score(q, fixtureWeights()), back.Score(q, fixtureWeights()))

	var again bytes.Buffer
	_, err = back.WriteTo(&again)
	require.NoError(t, err)
	assert.Equal(t, encoded, again.Bytes(), "the encoding depends only on the contents")
}

func TestIndexRoundTripRefusesAnotherVersion(t *testing.T) {
	data := append([]byte{}, indexMagic[:]...)
	data = binary.AppendUvarint(data, indexFormat+1)
	ix, err := ReadIndex(bytes.NewReader(data))
	require.Error(t, err)
	assert.Nil(t, ix)
	assert.Contains(t, err.Error(), "version 2")
	assert.Contains(t, err.Error(), "version 1")
}

func TestIndexRoundTripRefusesATruncatedIndex(t *testing.T) {
	var buf bytes.Buffer
	_, err := NewIndex(fixtureDocs()).WriteTo(&buf)
	require.NoError(t, err)

	for _, cut := range []int{0, 3, 5, buf.Len() / 2, buf.Len() - 1} {
		ix, err := ReadIndex(bytes.NewReader(buf.Bytes()[:cut]))
		assert.Error(t, err, "cut at %d", cut)
		assert.Nil(t, ix, "cut at %d", cut)
	}
}
