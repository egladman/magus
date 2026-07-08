package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
// exact-ID lookup loads only the shards that mention S. It is deterministic (a pure
// function of the symbol shards), rebuilt whenever they change, and never the source
// of truth - a missing or stale file just falls back to loading all symbol shards.

// symbolsRoutingFile is the derived xref index, written beside the shards. Keyed by a
// hash of the symbol node ID (compact at millions of symbols) to the sorted names of
// the shards whose index mentions that symbol.
const symbolsRoutingFile = "@symbols.routing.json"

// symbolRefKey hashes a symbol node ID to the routing file's compact key.
func symbolRefKey(symbolID string) string {
	sum := sha256.Sum256([]byte(symbolID))
	return hex.EncodeToString(sum[:8])
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

// writeXref persists the routing index (or removes a stale one when no symbols exist),
// so the file is present exactly when it is useful. Best-effort at the write layer:
// callers treat a routing failure as non-fatal (the graph still works, just without
// the reverse-lookup shortcut).
func (s *Store) writeXref(shards []Shard) error {
	xref := buildXref(shards)
	if len(xref) == 0 {
		if err := os.Remove(s.routingPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	b, err := codec.MarshalIndent(xref, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "shards"), 0o755); err != nil {
		return err
	}
	return file.WriteFileAtomic(s.routingPath(), b, 0o644)
}

// readXref loads the routing index, or nil when it is absent/unreadable (the caller
// then falls back to loading all symbol shards).
func (s *Store) readXref() map[string][]string {
	b, err := os.ReadFile(s.routingPath())
	if err != nil {
		return nil
	}
	var xref map[string][]string
	if err := codec.Unmarshal(b, &xref); err != nil {
		return nil
	}
	return xref
}

// MergeSymbolShardsFor merges only the @symbols shards that mention the given symbol
// IDs (per the routing index) into g, restoring an evicted shard from the remote as
// needed. It is the targeted counterpart to MergeSymbolShards: a `magus refs S` on an
// exact symbol ID loads a handful of shards instead of all of them. Falls back to a
// full symbol load when the routing file is missing (never built, or a fuzzy ref
// whose exact ID is not yet known).
func (s *Store) MergeSymbolShardsFor(ctx context.Context, g *Graph, symbolIDs []string) error {
	xref := s.readXref()
	if xref == nil {
		return s.MergeSymbolShards(ctx, g)
	}
	want := map[string]bool{}
	for _, id := range symbolIDs {
		for _, name := range xref[symbolRefKey(id)] {
			want[name] = true
		}
	}
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	slices.Sort(names)

	man := s.readManifestOrNil()
	if man == nil {
		return nil
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, ok := man.Shards[name]; !ok {
			continue // routing named a shard the manifest no longer has; skip
		}
		sf, err := s.readShard(name)
		if err != nil {
			if s.restoreShard(ctx, name, man.Shards[name].Fingerprint) == nil {
				sf, err = s.readShard(name)
			}
			if err != nil {
				return fmt.Errorf("knowledge: load symbol shard %q: %w", name, err)
			}
		}
		g.Merge(sf.Nodes, sf.Edges)
	}
	return nil
}
