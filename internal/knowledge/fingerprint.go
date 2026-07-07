package knowledge

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/types"
)

// Shards are fingerprinted by their assembled content: a shard is rewritten only
// when its nodes/edges actually change. Content-hashing (rather than hashing a
// project's source files) is what makes the fingerprint correct across ALL
// inputs - a cross-project dependency rename changes the dependent shard's edges
// and therefore its fingerprint, which per-project source hashing would miss.
//
// The tradeoff is that a shard must be assembled to be fingerprinted, so this
// does not yet skip assembly for unchanged shards; Build re-derives the whole
// graph each run and the fingerprint only governs writes. A Phase 8 optimization
// may add a cheap pre-assembly source hash to skip re-deriving unchanged shards,
// but that hash must cover cross-project and registry inputs, not just a
// project's own magusfiles.
//
// SHA256 matches the cache's hasher (benchmarked faster than BLAKE3 on this
// workload; see the hasher memory).

func fingerprintShardContent(sh Shard) (string, error) {
	g := NewGraph()
	g.Merge(sh.Nodes, sh.Edges)
	payload := struct {
		Nodes []types.KnowledgeNode `json:"nodes"`
		Edges []types.KnowledgeEdge `json:"edges"`
	}{g.Nodes(), g.Edges()}
	b, err := codec.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
