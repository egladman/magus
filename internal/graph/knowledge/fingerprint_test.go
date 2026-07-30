package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shardFor builds a one-node one-edge shard, so a test can vary a single field and
// assert the fingerprint moved because of that field and nothing else.
func shardFor(n types.KnowledgeNode, e types.KnowledgeEdge) Shard {
	return Shard{Nodes: []types.KnowledgeNode{n}, Edges: []types.KnowledgeEdge{e}}
}

func baseNode() types.KnowledgeNode {
	return types.KnowledgeNode{
		ID: "target:.:build", Kind: "target", Label: "build", Doc: "builds it",
		Source: "magusfile.buzz:12", Attrs: map[string]string{"engine": "buzz", "cache_hit_rate": "0.9"},
	}
}

func baseEdge() types.KnowledgeEdge {
	return types.KnowledgeEdge{
		Source: "target:.:build", Target: "op:go:go-build", Relation: "uses",
		Confidence: "extracted", Score: 0.75, Provenance: "magusfile.buzz:12",
	}
}

// TestShardFingerprintStable is the property the whole write-skipping mechanism
// rests on: identical content must hash identically, every run and every process.
// If it did not, Build would rewrite every shard on every command and the
// fingerprint would be pure cost.
func TestShardFingerprintStable(t *testing.T) {
	a, err := fingerprintShardContent(shardFor(baseNode(), baseEdge()))
	require.NoError(t, err)
	b, err := fingerprintShardContent(shardFor(baseNode(), baseEdge()))
	require.NoError(t, err)
	assert.Equal(t, a, b, "identical content must fingerprint identically")
	assert.Len(t, a, 64, "hex-encoded SHA256")
}

// TestShardFingerprintIgnoresInputOrder covers the other half: the fingerprint is of
// merged CONTENT, not of the order the assembler happened to emit it in. Two shards
// holding the same nodes in a different order are the same shard.
func TestShardFingerprintIgnoresInputOrder(t *testing.T) {
	n1 := baseNode()
	n2 := types.KnowledgeNode{ID: "target:.:test", Kind: "target", Label: "test"}

	fwd, err := fingerprintShardContent(Shard{Nodes: []types.KnowledgeNode{n1, n2}})
	require.NoError(t, err)
	rev, err := fingerprintShardContent(Shard{Nodes: []types.KnowledgeNode{n2, n1}})
	require.NoError(t, err)
	assert.Equal(t, fwd, rev, "Graph.Nodes sorts by ID, so emission order cannot leak in")
}

// TestShardFingerprintChangesPerField is the guard that would catch a field dropped
// from the hash. A field left out hashes two genuinely different shards alike, and
// the consequence is silent: the changed shard is never rewritten, so the graph
// serves stale content while reporting itself current.
//
// Every field is exercised individually rather than in one mutated blob, so a
// failure names which field stopped counting.
func TestShardFingerprintChangesPerField(t *testing.T) {
	base, err := fingerprintShardContent(shardFor(baseNode(), baseEdge()))
	require.NoError(t, err)

	nodeMutations := map[string]func(*types.KnowledgeNode){
		"ID":          func(n *types.KnowledgeNode) { n.ID = "target:.:other" },
		"Kind":        func(n *types.KnowledgeNode) { n.Kind = "op" },
		"Label":       func(n *types.KnowledgeNode) { n.Label = "changed" },
		"Doc":         func(n *types.KnowledgeNode) { n.Doc = "different doc" },
		"Source":      func(n *types.KnowledgeNode) { n.Source = "magusfile.buzz:99" },
		"attr value":  func(n *types.KnowledgeNode) { n.Attrs["engine"] = "other" },
		"attr key":    func(n *types.KnowledgeNode) { n.Attrs["added"] = "v" },
		"attr absent": func(n *types.KnowledgeNode) { delete(n.Attrs, "engine") },
	}
	for name, mutate := range nodeMutations {
		t.Run("node "+name, func(t *testing.T) {
			n := baseNode()
			mutate(&n)
			got, err := fingerprintShardContent(shardFor(n, baseEdge()))
			require.NoError(t, err)
			assert.NotEqual(t, base, got, "a changed node %s must change the fingerprint", name)
		})
	}

	edgeMutations := map[string]func(*types.KnowledgeEdge){
		"Source":     func(e *types.KnowledgeEdge) { e.Source = "target:.:test" },
		"Target":     func(e *types.KnowledgeEdge) { e.Target = "op:go:go-test" },
		"Relation":   func(e *types.KnowledgeEdge) { e.Relation = "needs" },
		"Confidence": func(e *types.KnowledgeEdge) { e.Confidence = "inferred" },
		"Score":      func(e *types.KnowledgeEdge) { e.Score = 0.76 },
		"Provenance": func(e *types.KnowledgeEdge) { e.Provenance = "elsewhere:1" },
	}
	for name, mutate := range edgeMutations {
		t.Run("edge "+name, func(t *testing.T) {
			e := baseEdge()
			mutate(&e)
			got, err := fingerprintShardContent(shardFor(baseNode(), e))
			require.NoError(t, err)
			assert.NotEqual(t, base, got, "a changed edge %s must change the fingerprint", name)
		})
	}
}

// TestShardFingerprintResistsFieldSplitCollision is why the fields are
// length-prefixed. Concatenating them raw would hash ("ab","c") and ("a","bc")
// identically, so a node whose label ended where the next field began could be
// swapped for a different node without the fingerprint noticing.
func TestShardFingerprintResistsFieldSplitCollision(t *testing.T) {
	left, err := fingerprintShardContent(Shard{Nodes: []types.KnowledgeNode{
		{ID: "a", Kind: "bc"},
	}})
	require.NoError(t, err)
	right, err := fingerprintShardContent(Shard{Nodes: []types.KnowledgeNode{
		{ID: "ab", Kind: "c"},
	}})
	require.NoError(t, err)
	assert.NotEqual(t, left, right, "the length prefix must keep a field split from colliding")
}

// BenchmarkFingerprintShard is the measurement this change is judged on. It was
// absent, which is how marshal-then-hash stayed on the hot path of every command:
// fingerprinting all shards costs 757 ms at 50k projects, and the encode existed
// only to be discarded.
func BenchmarkFingerprintShard(b *testing.B) {
	in := syntheticInputs(200, 8)
	shards := AssembleShards(in)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, sh := range shards {
			if _, err := fingerprintShardContent(sh); err != nil {
				b.Fatal(err)
			}
		}
	}
}
