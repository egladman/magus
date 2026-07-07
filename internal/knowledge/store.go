package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/types"
)

// Storage layout under <cacheDir>/knowledge:
//   manifest.json          per-shard fingerprints + counts (the routing index)
//   shards/<file>.json     one file per shard; SHARDS ARE AUTHORITATIVE
// There is no continuously maintained merged graph.json: at scale, rewriting a
// merged file on every shard change is an O(graph) write per edit. Merging
// happens in memory at load time; the merged node-link export is produced on
// demand by `magus graph export`.

// ErrNoStore reports that the knowledge store has never been written (no manifest).
var ErrNoStore = errors.New("knowledge: no persisted graph")

// StoreDir returns the knowledge-store directory for a resolved cache dir.
func StoreDir(cacheDir string) string { return filepath.Join(cacheDir, "knowledge") }

// manifest is the per-shard index persisted at manifest.json. It doubles as the
// (future) shard routing index; Phase 1 carries only fingerprints and counts.
type manifest struct {
	SchemaVersion int                  `json:"schema_version"`
	Shards        map[string]shardMeta `json:"shards"`
}

type shardMeta struct {
	Fingerprint string `json:"fingerprint"`
	NodeCount   int    `json:"node_count"`
	EdgeCount   int    `json:"edge_count"`
}

// shardFile is one shard's on-disk form. Name is stored so filenames never need
// to be parsed back into shard names (the manifest keys and this field are the
// source of truth); the filename is a derived, collision-free slug.
type shardFile struct {
	SchemaVersion int                   `json:"schema_version"`
	Name          string                `json:"name"`
	Fingerprint   string                `json:"fingerprint"`
	Nodes         []types.KnowledgeNode `json:"nodes"`
	Edges         []types.KnowledgeEdge `json:"edges"`
}

// Store persists and loads knowledge shards. It is the cache-first backing: a
// query loads shards, fingerprint-checks them, rebuilds only what is stale.
type Store struct {
	dir       string
	immutable bool
	log       *slog.Logger
}

// NewStore returns a store rooted at <cacheDir>/knowledge. immutable mirrors
// MAGUS_CACHE_IMMUTABLE: when set, Sync writes nothing and warns if stale.
func NewStore(cacheDir string, immutable bool, log *slog.Logger) *Store {
	if log == nil {
		log = slog.Default()
	}
	return &Store{dir: StoreDir(cacheDir), immutable: immutable, log: log}
}

// Sync reconciles freshly-assembled shards against the persisted store and
// returns the merged in-memory graph. It writes only shards whose fingerprint
// changed, prunes shards no longer present (free deletion/rename reconciliation),
// and rewrites the manifest. In immutable mode it writes nothing but still
// returns the merged graph, warning once if the persisted store is stale.
// refresh forces every shard to be treated as stale (a full rebuild).
func (s *Store) Sync(ctx context.Context, shards []Shard, fps map[string]string, refresh bool) (*Graph, error) {
	old := s.readManifestOrNil()
	if refresh {
		old = nil
	}
	newMan := manifest{SchemaVersion: types.KnowledgeSchemaVersion, Shards: map[string]shardMeta{}}
	g := NewGraph()

	present := make(map[string]bool, len(shards))
	changed := false
	var toWrite []shardWrite
	for _, sh := range shards {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		present[sh.Name] = true
		fp := fps[sh.Name]
		g.Merge(sh.Nodes, sh.Edges)
		newMan.Shards[sh.Name] = shardMeta{Fingerprint: fp, NodeCount: len(sh.Nodes), EdgeCount: len(sh.Edges)}

		prev, ok := old.shard(sh.Name)
		unchanged := ok && prev.Fingerprint == fp && s.shardExists(sh.Name)
		if unchanged {
			continue
		}
		changed = true
		if s.immutable {
			continue
		}
		toWrite = append(toWrite, shardWrite{shard: sh, fp: fp})
	}

	if s.immutable {
		// Warn only when a prior store exists and diverges - a first-ever run
		// under immutable mode is uninitialized, not stale.
		if old != nil && (changed || old.prunable(present)) {
			s.log.Warn("magus: knowledge graph is stale but MAGUS_CACHE_IMMUTABLE is set; serving a freshly assembled in-memory graph without persisting")
		}
		return g, nil
	}

	if err := s.writeShards(ctx, toWrite); err != nil {
		return nil, err
	}

	// optimization: on a no-op rebuild (nothing changed, nothing to prune) the
	// on-disk manifest already matches, so skip rewriting it. This is the common
	// steady-state path - every query rebuilds the graph - and it removes an
	// atomic write (temp+rename) plus keeps the manifest's mtime stable.
	//   measured: folded into the BenchmarkBuildNoop delta above; removes the
	//             one guaranteed write from the otherwise write-free hot path.
	//   trade-off: none; the manifest is only skipped when it would be identical.
	if !changed && !old.prunable(present) {
		return g, nil
	}

	// Write the manifest before pruning the shards it no longer references: the
	// manifest is the index, so a crash after it lands leaves only orphan shard
	// files (ignored by Load) rather than a manifest pointing at a deleted shard.
	if err := s.writeManifest(newMan); err != nil {
		return nil, err
	}
	if old != nil {
		for name := range old.Shards {
			if !present[name] {
				if err := s.removeShard(name); err != nil {
					return nil, err
				}
			}
		}
	}
	return g, nil
}

// Load reads the persisted graph from disk without any assembly. Returns
// ErrNoStore when the store has never been written. Used for the cache-only fast
// path (a warm store answers without touching workspace sources).
func (s *Store) Load(ctx context.Context) (*Graph, error) {
	man := s.readManifestOrNil()
	if man == nil {
		return nil, ErrNoStore
	}
	g := NewGraph()
	for name := range man.Shards {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sf, err := s.readShard(name)
		if err != nil {
			return nil, fmt.Errorf("knowledge: load shard %q: %w", name, err)
		}
		g.Merge(sf.Nodes, sf.Edges)
	}
	return g, nil
}

// --- manifest / shard IO ---

func (s *Store) manifestPath() string { return filepath.Join(s.dir, "manifest.json") }

func (s *Store) shardPath(name string) string {
	return filepath.Join(s.dir, "shards", shardSlug(name)+".json")
}

func (s *Store) shardExists(name string) bool {
	_, err := os.Stat(s.shardPath(name))
	return err == nil
}

func (s *Store) readManifestOrNil() *manifest {
	b, err := os.ReadFile(s.manifestPath())
	if err != nil {
		return nil
	}
	var m manifest
	if err := codec.Unmarshal(b, &m); err != nil {
		return nil // a corrupt manifest is treated as absent; a full rebuild follows
	}
	if m.SchemaVersion != types.KnowledgeSchemaVersion {
		return nil // schema bump invalidates the whole store
	}
	return &m
}

func (s *Store) writeManifest(m manifest) error {
	b, err := codec.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return file.WriteFileAtomic(s.manifestPath(), b, 0o644)
}

// shardWrite pairs a shard with its precomputed fingerprint for the write phase.
type shardWrite struct {
	shard Shard
	fp    string
}

// writeShards writes every pending shard, in parallel. A cold build of a large
// monorepo writes thousands of independent shard files; serial atomic writes
// (temp + rename) are I/O-latency bound, so overlapping them is a large win.
//
// optimization: fan the per-shard writeShard calls across bounded goroutines.
//
//	measured: BenchmarkBuildCold -26.5% sec/op in isolation (benchstat, n=8+6,
//	          2000-project fixture: ~10.3s -> ~7.6s). Atomic writes (temp+rename)
//	          are the largest single cost; the rest is the per-shard marshal/sort/
//	          hash, parallelized separately in Build's fingerprint pass.
//	trade-off: shard writes are unordered; correctness is unaffected because each
//	          targets a distinct file and the manifest is written afterward.
func (s *Store) writeShards(ctx context.Context, writes []shardWrite) error {
	if len(writes) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "shards"), 0o755); err != nil {
		return err
	}
	eg, _ := errgroup.WithContext(ctx)
	eg.SetLimit(runtime.GOMAXPROCS(0) * 2)
	for _, w := range writes {
		eg.Go(func() error { return s.writeShard(w.shard, w.fp) })
	}
	return eg.Wait()
}

func (s *Store) writeShard(sh Shard, fp string) error {
	// Persist in canonical sorted order so identical inputs produce byte-identical
	// files (diffable, and the content fingerprint is stable).
	g := NewGraph()
	g.Merge(sh.Nodes, sh.Edges)
	sf := shardFile{
		SchemaVersion: types.KnowledgeSchemaVersion,
		Name:          sh.Name,
		Fingerprint:   fp,
		Nodes:         g.Nodes(),
		Edges:         g.Edges(),
	}
	b, err := codec.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	return file.WriteFileAtomic(s.shardPath(sh.Name), b, 0o644)
}

func (s *Store) readShard(name string) (shardFile, error) {
	b, err := os.ReadFile(s.shardPath(name))
	if err != nil {
		return shardFile{}, err
	}
	var sf shardFile
	if err := codec.Unmarshal(b, &sf); err != nil {
		return shardFile{}, err
	}
	return sf, nil
}

func (s *Store) removeShard(name string) error {
	err := os.Remove(s.shardPath(name))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// shardSlug maps a shard name to a filesystem-safe, collision-free filename:
// a readable prefix plus a short hash of the full name. The name itself is
// stored inside the file and keyed in the manifest, so the slug is never parsed
// back - it only needs to be deterministic and unique.
func shardSlug(name string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
	if len(safe) > 40 {
		safe = safe[:40]
	}
	sum := sha256.Sum256([]byte(name))
	return safe + "-" + hex.EncodeToString(sum[:])[:8]
}

// shard looks up one shard's persisted meta; the nil receiver (no manifest yet)
// reports absent so first-run assembly writes everything.
func (m *manifest) shard(name string) (shardMeta, bool) {
	if m == nil {
		return shardMeta{}, false
	}
	sm, ok := m.Shards[name]
	return sm, ok
}

// prunable reports whether the manifest names any shard absent from present,
// i.e. whether a prune would occur.
func (m *manifest) prunable(present map[string]bool) bool {
	if m == nil {
		return false
	}
	for name := range m.Shards {
		if !present[name] {
			return true
		}
	}
	return false
}
