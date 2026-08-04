package knowledge

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// Load must merge shards in a stable order. AddNode and AddEdge are both
// first-writer-wins, so when two shards describe the same node with a different
// Source, the winner is decided purely by merge order - and Load used to take Go's
// randomized map order over the manifest.
//
// The failure this guards against is nasty precisely because it is quiet: the export
// sorts nodes and edges by ID afterward, so counts never move and the diff is a
// handful of "source" lines. That is exactly how it presented - `magus run generate`
// regenerated gen/*.json, the drift gate saw a changed file, and the change was
// provenance only.
//
// Many shards and many iterations because map order is random per range: two shards
// would coin-flip and could pass a short run by luck.
func TestLoadMergesShardsInStableOrder(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), ".magus")
	store := NewStore(cacheDir, false, 0, nil, nil)
	ctx := context.Background()

	// Every shard claims the same node and the same edge, each with its own Source.
	// Whichever merges first supplies both, so the merged result names the winner.
	const shardCount = 12
	shards := make([]Shard, 0, shardCount)
	fps := map[string]string{}
	for i := range shardCount {
		name := fmt.Sprintf("@s%02d", i)
		sh := Shard{
			Name: name,
			Nodes: []types.KnowledgeNode{
				{ID: "file:shared.go", Kind: types.KindFile, Source: fmt.Sprintf("shard-%02d.go", i)},
				{ID: fmt.Sprintf("file:own%02d.go", i), Kind: types.KindFile, Source: "own.go"},
			},
			Edges: []types.KnowledgeEdge{{
				Source:     "file:shared.go",
				Target:     fmt.Sprintf("file:own%02d.go", i),
				Relation:   types.RelationReferences,
				Confidence: types.ConfidenceExtracted,
				Provenance: fmt.Sprintf("shard-%02d.go", i),
			}},
		}
		shards = append(shards, sh)
		fps[name] = fingerprintShardContent(sh)
	}
	_, err := store.Sync(ctx, shards, fps, nil, false)
	require.NoError(t, err)

	// Load repeatedly from the SAME on-disk store; every read must agree.
	var want string
	for i := range 25 {
		g, err := NewStore(cacheDir, false, 0, nil, nil).Load(ctx)
		require.NoError(t, err)
		n, ok := g.node("file:shared.go")
		require.True(t, ok, "shared node missing on iteration %d", i)
		if i == 0 {
			want = n.Source
			continue
		}
		assert.Equal(t, want, n.Source,
			"iteration %d disagreed on provenance: shard merge order is not stable", i)
		if t.Failed() {
			break
		}
	}

	// The sorted order is the contract, not merely "some fixed order": the first
	// shard by name wins, which is what makes the result predictable to a reader
	// rather than an artifact of whatever the map happened to yield.
	assert.Equal(t, "shard-00.go", want, "the lowest-named shard should win a first-writer-wins merge")
}
