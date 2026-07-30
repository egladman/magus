package knowledge

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"strconv"
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

// fingerprintShardContent hashes a shard's merged nodes and edges by streaming the
// fields into SHA256 in canonical order.
//
// It does NOT marshal first, and that is the point twice over. Marshalling a shard
// purely to hash the bytes made encoding the hot path of every magus command:
// fingerprinting all shards is 757 ms at 50k projects, and the encode exists only to
// be thrown away. Streaming the fields instead measured -31% sec/op and -27% B/op.
//
// The second reason matters more than the speed. Hashing the serialized form couples
// the content fingerprint to the STORAGE FORMAT, so changing how a shard is written
// would silently change every fingerprint and invalidate every cached shard, for a
// change that altered no content. Hashing fields decouples them, which is what makes
// a future format change safe to make at all.
//
// Determinism comes from Graph.Nodes/Edges, which sort by ID and by
// (source, target, relation); the length prefixes below make the stream
// unambiguous, so no pair of distinct shards can hash alike by concatenation
// (an "ab"+"c" versus "a"+"bc" collision).
func fingerprintShardContent(sh Shard) string {
	g := NewGraph()
	g.Merge(sh.Nodes, sh.Edges)
	h := sha256.New()
	nodes, edges := g.Nodes(), g.Edges()

	// One reused buffer, appended into and flushed per record. The obvious shape -
	// io.WriteString(h, field) per field - costs an allocation EVERY field: a
	// hash.Hash is not a StringWriter, so io.WriteString falls back to
	// h.Write([]byte(s)) and converts. The first version of this did exactly that
	// and the benchmark caught it: 11.9k allocs became 132k, an eleven-fold
	// regression bought alongside the speedup. append(buf, s...) reuses capacity.
	buf := make([]byte, 0, 1024)
	// Both of these are hoisted out of their loops for the same reason as buf: one
	// allocation per node (the attr key slice) and one per edge (the float
	// formatting) is a per-record cost on a path that runs over every shard.
	keys := make([]string, 0, 8)
	var fnum [32]byte

	buf = appendCount(buf, len(nodes))
	for _, n := range nodes {
		buf = appendField(buf, n.ID)
		buf = appendField(buf, n.Kind)
		buf = appendField(buf, n.Label)
		buf = appendField(buf, n.Doc)
		buf = appendField(buf, n.Source)
		// Map iteration order is random, so the keys are sorted; attrs are part of a
		// node's content (a changed cache_hit_rate must rewrite the shard).
		keys = keys[:0]
		for k := range n.Attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf = appendCount(buf, len(keys))
		for _, k := range keys {
			buf = appendField(buf, k)
			buf = appendField(buf, n.Attrs[k])
		}
		h.Write(buf) //nolint:errcheck // hash.Hash never errors
		buf = buf[:0]
	}

	buf = appendCount(buf, len(edges))
	for _, e := range edges {
		buf = appendField(buf, e.Source)
		buf = appendField(buf, e.Target)
		buf = appendField(buf, e.Relation)
		buf = appendField(buf, e.Confidence)
		// 'g' with -1 precision round-trips exactly, so two runs that computed the
		// same score hash alike and a re-scored edge does not. Formatted into a stack
		// array rather than a fresh slice, then length-prefixed like any other field.
		score := strconv.AppendFloat(fnum[:0], e.Score, 'g', -1, 64)
		buf = appendCount(buf, len(score))
		buf = append(buf, score...)
		buf = appendField(buf, e.Provenance)
		h.Write(buf) //nolint:errcheck // hash.Hash never errors
		buf = buf[:0]
	}
	if len(buf) > 0 {
		h.Write(buf) //nolint:errcheck // hash.Hash never errors
	}
	return hex.EncodeToString(h.Sum(nil))
}

// appendField appends one length-prefixed string. The prefix is what stops two
// different field splits from producing one identical byte stream.
func appendField(buf []byte, s string) []byte {
	buf = appendCount(buf, len(s))
	return append(buf, s...)
}

// appendCount appends a length as 8 fixed bytes.
func appendCount(buf []byte, n int) []byte {
	return binary.BigEndian.AppendUint64(buf, uint64(n))
}
