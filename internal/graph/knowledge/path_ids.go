package knowledge

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
)

// pathIDsFile is an index file holding each shard's file and dir node IDs, bound to the
// fingerprint they were read at. The @session overlay resolves contacts against exactly
// those IDs across every class, so reassembling it alone reads this instead of decoding
// every stored symbol shard to learn which files exist.
const pathIDsFile = "paths.json"

type pathIDEntry struct {
	Fingerprint string   `json:"fingerprint"`
	IDs         []string `json:"ids"`
}

func (s *Store) pathIDsPath() string { return filepath.Join(s.dir, pathIDsFile) }

// isPathID reports whether id names a file or dir node, the only kinds assembleSession
// looks up.
func isPathID(id string) bool {
	return strings.HasPrefix(id, types.KindFile+":") || strings.HasPrefix(id, types.KindDir+":")
}

func collectPathIDs(nodes []types.KnowledgeNode) []string {
	var ids []string
	for _, n := range nodes {
		if isPathID(n.ID) {
			ids = append(ids, n.ID)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

func (s *Store) readPathIDs() map[string]pathIDEntry {
	b, err := os.ReadFile(s.pathIDsPath())
	if err != nil {
		return nil
	}
	var idx map[string]pathIDEntry
	if json.Unmarshal(b, &idx) != nil {
		return nil
	}
	return idx
}

// recordPathIDs brings the path ID index in line with man: entries for shards it no longer
// names, or names at another fingerprint, are dropped, and every synced shard is indexed.
// Best-effort: a path ID index that is missing or behind only costs the next overlay rebuild a
// read of the shards it lacks.
func (s *Store) recordPathIDs(ctx context.Context, shards []Shard, fps map[string]string, man manifest) {
	old := s.readPathIDs()
	next := make(map[string]pathIDEntry, len(man.Shards))
	for name, meta := range man.Shards {
		if e, ok := old[name]; ok && meta.Fingerprint != "" && e.Fingerprint == meta.Fingerprint {
			next[name] = e
		}
	}
	for _, sh := range shards {
		fp := fps[sh.Name]
		if fp == "" {
			continue // an unfingerprinted shard cannot be told apart from its next version
		}
		if e, ok := next[sh.Name]; ok && e.Fingerprint == fp {
			continue
		}
		next[sh.Name] = pathIDEntry{Fingerprint: fp, IDs: collectPathIDs(sh.Nodes)}
	}
	if maps.EqualFunc(old, next, func(a, b pathIDEntry) bool {
		return a.Fingerprint == b.Fingerprint && slices.Equal(a.IDs, b.IDs)
	}) {
		return
	}
	b, err := json.Marshal(next)
	if err == nil {
		err = file.WriteFileAtomic(s.pathIDsPath(), b, 0o644)
	}
	if err != nil {
		s.log.DebugContext(ctx, "path ID index write failed", attr.Error(err))
	}
}

// storedPathIDs returns the file and dir node IDs of every stored shard whose class is
// not in skip, from the path ID index where it is current and from the shard file otherwise.
//
// A shard rewritten after man was read restarts the walk once from a fresh
// manifest. A store still moving on the second pass fails.
func (s *Store) storedPathIDs(ctx context.Context, man *manifest, skip []ShardClass) (map[string]bool, error) {
	out, err := s.walkStoredPathIDs(ctx, man, skip)
	if !errors.Is(err, errShardMoved) {
		return out, err
	}
	fresh := s.readManifestOrNil()
	if fresh == nil {
		return nil, err
	}
	return s.walkStoredPathIDs(ctx, fresh, skip)
}

func (s *Store) walkStoredPathIDs(ctx context.Context, man *manifest, skip []ShardClass) (map[string]bool, error) {
	out := map[string]bool{}
	if man == nil {
		return out, nil
	}
	idx := s.readPathIDs()
	for _, name := range slices.Sorted(maps.Keys(man.Shards)) {
		if slices.Contains(skip, shardClass(name)) {
			continue
		}
		meta := man.Shards[name]
		if e, ok := idx[name]; ok && meta.Fingerprint != "" && e.Fingerprint == meta.Fingerprint {
			for _, id := range e.IDs {
				out[id] = true
			}
			continue
		}
		sf, err := s.readVerifiedShard(ctx, man, name)
		if err != nil {
			return nil, fmt.Errorf("read shard %q for the overlay's node set: %w", name, err)
		}
		for _, id := range collectPathIDs(sf.Nodes) {
			out[id] = true
		}
	}
	return out, nil
}
