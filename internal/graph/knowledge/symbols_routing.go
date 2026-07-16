package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/types"
)

// The symbol xref routing file is a DERIVED index for scale-safe reverse lookup:
// which @symbols shards must be loaded to see a given symbol's definition and every
// reference to it. Without it, `magus refs S` loads every symbol shard; with it, an
// exact-ID lookup loads only the shards that mention S. It is a pure function of the
// symbol shards, and it is NEVER the source of truth: a missing, corrupt, STALE, or
// unhelpful routing file just falls back to loading all symbol shards, so it can only
// ever make a lookup faster, never wrong.

// symbolsRoutingFile is the derived xref index, written beside the shards.
const symbolsRoutingFile = "@symbols.routing.json"

// symbolRouting is the persisted index plus the key that binds it to the exact symbol
// shards it was built from. On read, a ShardsKey that does not match the current
// manifest means the shards moved since the file was written (a swallowed write, an
// immutable run, or a crash between the manifest and this file) - it is stale and
// ignored, so a stale file degrades to load-all rather than an under-load.
type symbolRouting struct {
	// ShardsKey hashes the current symbol shards' (name, fingerprint); the Index is
	// valid exactly while it is unchanged.
	ShardsKey string `json:"shards_key"`
	// Index maps a symbol-id hash (compact at millions of symbols) to the sorted names
	// of the shards whose index mentions that symbol.
	Index map[string][]string `json:"index"`
}

// symbolRefKey hashes a symbol node ID to the routing index's compact key.
func symbolRefKey(symbolID string) string {
	sum := sha256.Sum256([]byte(symbolID))
	return hex.EncodeToString(sum[:8])
}

// symbolShardsKey hashes the sorted (name, fingerprint) of every symbol shard in the
// manifest - the identity the routing index is bound to. Empty when there are none.
func symbolShardsKey(man *manifest) string {
	if man == nil {
		return ""
	}
	var names []string
	for name := range man.Shards {
		if IsSymbolsShard(name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	slices.Sort(names)
	h := sha256.New()
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(man.Shards[name].Fingerprint))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// buildXref indexes every symbol node in the given shards to the shard names it
// appears in: the defining shard plus every shard that references it (assembleSymbols
// emits a symbol node wherever an index mentions the symbol, defined or referenced).
// Shard-name lists are sorted for deterministic output.
func buildXref(shards []Shard) map[string][]string {
	byKey := map[string]map[string]bool{}
	for _, sh := range shards {
		if !IsSymbolsShard(sh.Name) {
			continue
		}
		for _, n := range sh.Nodes {
			if n.Kind != types.KindSymbol {
				continue
			}
			key := symbolRefKey(n.ID)
			if byKey[key] == nil {
				byKey[key] = map[string]bool{}
			}
			byKey[key][sh.Name] = true
		}
	}
	out := make(map[string][]string, len(byKey))
	for key, shardSet := range byKey {
		names := make([]string, 0, len(shardSet))
		for name := range shardSet {
			names = append(names, name)
		}
		slices.Sort(names)
		out[key] = names
	}
	return out
}

func (s *Store) routingPath() string { return filepath.Join(s.dir, "shards", symbolsRoutingFile) }

// writeXref persists the routing index bound to man's symbol shards (or removes a
// stale file when no symbols exist, so it is present exactly when useful). Best-effort
// at the write layer: callers treat a failure as non-fatal (the graph still works,
// lookups just fall back to loading all symbol shards).
func (s *Store) writeXref(shards []Shard, man manifest) error {
	index := buildXref(shards)
	if len(index) == 0 {
		if err := os.Remove(s.routingPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	b, err := codec.MarshalIndent(symbolRouting{ShardsKey: symbolShardsKey(&man), Index: index}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "shards"), 0o755); err != nil {
		return err
	}
	return file.WriteFileAtomic(s.routingPath(), b, 0o644)
}

// readXref loads the routing index, or nil when it is absent or unreadable.
func (s *Store) readXref() *symbolRouting {
	b, err := os.ReadFile(s.routingPath())
	if err != nil {
		return nil
	}
	var r symbolRouting
	if err := codec.Unmarshal(b, &r); err != nil {
		return nil
	}
	return &r
}

// MergeSymbolShardsByID merges only the @symbols shards that mention the given symbol
// IDs into g, for a scale-safe reverse lookup (`magus refs S` on an exact ID loads a
// handful of shards, not all). It falls back to a FULL symbol load - never an
// under-load - whenever the routing index cannot be trusted to be both fresh and
// helpful: absent, corrupt, stale (its ShardsKey no longer matches the manifest), or
// yielding no shards for these ids (a fuzzy symbol:-prefixed ref, or an unknown id).
func (s *Store) MergeSymbolShardsByID(ctx context.Context, g *Graph, symbolIDs []string) error {
	man := s.readManifestOrNil()
	if man == nil {
		return nil
	}
	routing := s.readXref()
	if routing == nil || routing.ShardsKey != symbolShardsKey(man) {
		return s.MergeSymbolShards(ctx, g)
	}
	want := map[string]bool{}
	for _, id := range symbolIDs {
		for _, name := range routing.Index[symbolRefKey(id)] {
			want[name] = true
		}
	}
	if len(want) == 0 {
		return s.MergeSymbolShards(ctx, g)
	}
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, ok := man.Shards[name]; !ok {
			continue // routing named a shard the manifest no longer has; skip
		}
		if err := s.readMergeShard(ctx, g, man, name); err != nil {
			return err
		}
	}
	// The coverage overlay is a single small shard, so load it whenever symbols are
	// pulled in - the routed subset still gets the ratio on the nodes it merged.
	return s.mergeCoverageShard(ctx, g, man)
}
