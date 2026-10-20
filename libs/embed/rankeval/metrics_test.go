package rankeval

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The expected values follow trec_eval's recall, recip_rank and ndcg_cut definitions,
// worked by hand for gold {a:3 b:2 c:1 d:1}.
var gold = map[string]int{"a": 3, "b": 2, "c": 1, "d": 1}

func TestRecallAt(t *testing.T) {
	ranked := []string{"a", "x", "b", "y", "c"}
	assert.InDelta(t, 0.25, RecallAt(ranked, gold, 1), 1e-12)
	assert.InDelta(t, 0.5, RecallAt(ranked, gold, 3), 1e-12)
	assert.InDelta(t, 0.75, RecallAt(ranked, gold, 5), 1e-12)
	assert.InDelta(t, 0.75, RecallAt(ranked, gold, 50), 1e-12, "k past the end")
	assert.InDelta(t, 0.25, RecallAt([]string{"a", "a", "a"}, gold, 3), 1e-12, "a repeat counts once")
	assert.Zero(t, RecallAt(nil, gold, 10))
	assert.Zero(t, RecallAt(ranked, nil, 10))
}

func TestReciprocalRank(t *testing.T) {
	assert.InDelta(t, 1.0, ReciprocalRank([]string{"a", "b"}, gold), 1e-12)
	assert.InDelta(t, 1.0/3, ReciprocalRank([]string{"x", "y", "c"}, gold), 1e-12)
	assert.Zero(t, ReciprocalRank([]string{"x", "y"}, gold))
	assert.Zero(t, ReciprocalRank(nil, gold))
}

func TestNDCGAt(t *testing.T) {
	// DCG@5 of a,x,b,y,c = 7/log2(2) + 3/log2(4) + 1/log2(6); the ideal ranks gold
	// 3,2,1,1 = 7/log2(2) + 3/log2(3) + 1/log2(4) + 1/log2(5).
	assert.InDelta(t, 0.9046555432847422, NDCGAt([]string{"a", "x", "b", "y", "c"}, gold, 5), 1e-12)
	// At k=2 the ideal is cut to 7 + 3/log2(3).
	assert.InDelta(t, 0.7871546029909718, NDCGAt([]string{"a", "x", "b"}, gold, 2), 1e-12)
	// The second b gains nothing: 3/log2(3) + 7/log2(5) over the full ideal.
	assert.InDelta(t, 0.49957166419231475, NDCGAt([]string{"x", "b", "b", "a"}, gold, 10), 1e-12)
	assert.InDelta(t, 1.0, NDCGAt([]string{"a", "b", "c", "d"}, gold, 10), 1e-12)
	assert.Zero(t, NDCGAt([]string{"x"}, gold, 10))
	assert.Zero(t, NDCGAt([]string{"a"}, nil, 10))
}
