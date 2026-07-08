package knowledge

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/types"
)

// ErrShardMiss reports that a shard key is not on the remote. GetShard returns it
// (not a nil reader) for a miss, so the contract is unambiguous: a nil error means
// a non-nil reader.
var ErrShardMiss = errors.New("knowledge: shard not on remote")

// RemoteShards lets the store ride a remote cache backend: shards are
// content-addressed by fingerprint, so Put is idempotent and Get restores an
// evicted shard by the same key. GetShard returns a non-nil reader on a hit, or
// ErrShardMiss on a miss. Nil = local-only.
type RemoteShards interface {
	GetShard(ctx context.Context, key string) (io.ReadCloser, error)
	PutShard(ctx context.Context, key string, r io.Reader) error
}

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
	maxBytes  int64        // soft cap on the shards dir; 0 = unlimited (default)
	remote    RemoteShards // optional shard backing; nil = local-only
	log       *slog.Logger
}

// NewStore returns a store rooted at <cacheDir>/knowledge. immutable mirrors
// MAGUS_CACHE_IMMUTABLE (Sync writes nothing, warns if stale). maxBytes soft-caps
// the shards dir (0 = unlimited); remote optionally backs shards (nil = local).
func NewStore(cacheDir string, immutable bool, maxBytes int64, remote RemoteShards, log *slog.Logger) *Store {
	if log == nil {
		log = slog.Default()
	}
	return &Store{dir: StoreDir(cacheDir), immutable: immutable, maxBytes: maxBytes, remote: remote, log: log}
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
		// Symbol shards are PERSISTED (fingerprinted, written, manifested) but NOT
		// merged into the default graph: they can dwarf the domain graph, so a query
		// that needs them loads them lazily (MergeSymbolShards). The @symbols name
		// suffix is the routing marker.
		if !IsSymbolsShard(sh.Name) {
			g.Merge(sh.Nodes, sh.Edges)
		}
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
	// Refresh the derived symbol xref routing index (best-effort: a failure just
	// means `magus refs` falls back to loading all symbol shards, never a wrong result).
	if err := s.writeXref(shards); err != nil {
		s.log.Debug("knowledge: symbol xref routing write failed", slog.String("error", err.Error()))
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
	// Enforce the soft size cap last, once every current shard is on disk, so the
	// newest shards survive and only cold ones are evicted.
	s.pruneToSize()
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
		if IsSymbolsShard(name) {
			continue // lazily loaded via MergeSymbolShards, not part of the default graph
		}
		sf, err := s.readShard(name)
		if err != nil {
			// The file may have been LRU-evicted while its manifest entry stayed.
			// Restore it from remote by fingerprint before giving up.
			if s.restoreShard(ctx, name, man.Shards[name].Fingerprint) == nil {
				sf, err = s.readShard(name)
			}
			if err != nil {
				return nil, fmt.Errorf("knowledge: load shard %q: %w", name, err)
			}
		}
		g.Merge(sf.Nodes, sf.Edges)
	}
	return g, nil
}

// MergeSymbolShards merges every persisted @symbols shard into g in place, restoring
// an LRU-evicted shard from the remote by fingerprint if needed. It is the on-demand
// half of lazy symbol loading: the default graph (Sync/Load) omits symbol shards for
// scale, and a symbol-seeded query calls this to pull them in. Best-effort by design:
// no store yet is not an error (a workspace that never ingested symbols just finds
// nothing), but a present-but-unreadable shard is surfaced.
func (s *Store) MergeSymbolShards(ctx context.Context, g *Graph) error {
	man := s.readManifestOrNil()
	if man == nil {
		return nil
	}
	// Merge in sorted shard order: if a symbol ID appears in two projects' shards
	// with a different label/source, AddNode is first-writer-wins, so a stable order
	// keeps the merged node deterministic (the domain Sync path merges a sorted slice).
	names := make([]string, 0, len(man.Shards))
	for name := range man.Shards {
		if IsSymbolsShard(name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
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

// restoreShard pulls a shard file from the remote backend by fingerprint and
// writes it locally, so an LRU-evicted (or never-fetched) shard is recovered
// without a rebuild. Returns an error when there is no remote or the pull fails.
func (s *Store) restoreShard(ctx context.Context, name, fp string) error {
	if s.remote == nil || fp == "" {
		return errors.New("knowledge: no remote to restore from")
	}
	rc, err := s.remote.GetShard(ctx, fp)
	if err != nil {
		return err // ErrShardMiss on a miss, or a real transport error
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "shards"), 0o755); err != nil {
		return err
	}
	return file.WriteFileAtomic(s.shardPath(name), b, 0o644)
}

// pruneToSize evicts least-recently-used shard FILES until the shards directory is
// within maxBytes, keeping their manifest entries so an evicted shard is restored
// from remote (restoreShard) or rebuilt from memory on the next sync. Newly
// written shards have the newest mtime, so they are evicted last. A no-op when no
// cap is set. Never evicts the manifest itself.
func (s *Store) pruneToSize() {
	if s.maxBytes <= 0 {
		return
	}
	dir := filepath.Join(s.dir, "shards")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return // no shards dir yet; nothing to prune
	}
	type shardStat struct {
		path  string
		size  int64
		mtime int64
	}
	var files []shardStat
	var total int64
	for _, e := range ents {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		files = append(files, shardStat{filepath.Join(dir, e.Name()), info.Size(), info.ModTime().UnixNano()})
		total += info.Size()
	}
	if total <= s.maxBytes {
		return
	}
	slices.SortFunc(files, func(a, b shardStat) int { return cmp.Compare(a.mtime, b.mtime) }) // oldest first
	for _, f := range files {
		if total <= s.maxBytes {
			break
		}
		if err := os.Remove(f.path); err != nil {
			continue
		}
		total -= f.size
	}
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
	eg, egctx := errgroup.WithContext(ctx)
	eg.SetLimit(runtime.GOMAXPROCS(0) * 2)
	for _, w := range writes {
		eg.Go(func() error { return s.writeShard(egctx, w.shard, w.fp) })
	}
	return eg.Wait()
}

func (s *Store) writeShard(ctx context.Context, sh Shard, fp string) error {
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
	if err := file.WriteFileAtomic(s.shardPath(sh.Name), b, 0o644); err != nil {
		return err
	}
	s.pushShard(ctx, sh.Name, fp, b)
	return nil
}

// remotePushTimeout bounds a shard push so a slow or hung remote cannot stall the
// build (Sync is on the cache-first query path). Pushes run in parallel, so a dead
// remote costs at most this once per rebuild-with-changes, not per shard.
const remotePushTimeout = 15 * time.Second

// pushShard best-effort uploads a non-runtime shard to the remote, keyed by its
// content fingerprint, so teammates and CI can restore it. A remote error or slow
// backend is logged and dropped: the local write already succeeded.
func (s *Store) pushShard(ctx context.Context, name, fp string, b []byte) {
	if s.remote == nil || IsRuntimeShard(name) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, remotePushTimeout)
	defer cancel()
	if err := s.remote.PutShard(ctx, fp, bytes.NewReader(b)); err != nil {
		s.log.Debug("knowledge: remote shard push failed", slog.String("shard", name), slog.String("error", err.Error()))
	}
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

// --- runtime diagnostic records (the @runtime shard's persisted input) ---

// defaultRuntimeCap bounds how many distinct (unit, code) records are retained, so
// run history cannot grow the store without limit. Oldest records drop first.
const defaultRuntimeCap = 5000

// runtimeRecordsPath is where runtime diagnostic records live: next to the shards,
// but not among them (it is the @runtime shard's input, not an output).
func runtimeRecordsPath(cacheDir string) string {
	return filepath.Join(StoreDir(cacheDir), "runtime.json")
}

// LoadRuntimeEvents reads the persisted runtime diagnostic records; a missing or
// unreadable file yields no events (runtime enrichment is best-effort).
func LoadRuntimeEvents(cacheDir string) []types.DiagnosticEvent {
	b, err := os.ReadFile(runtimeRecordsPath(cacheDir))
	if err != nil {
		return nil
	}
	var evs []types.DiagnosticEvent
	if err := codec.Unmarshal(b, &evs); err != nil {
		return nil
	}
	return evs
}

// RecordRuntimeEvents merges fresh events into the persisted records, deduped by
// (unit, code) and capped at defaultRuntimeCap (oldest dropped). Called once at run
// end; best-effort (an unwritable cache is not fatal).
func RecordRuntimeEvents(cacheDir string, fresh []types.DiagnosticEvent) error {
	if len(fresh) == 0 {
		return nil
	}
	merged := LoadRuntimeEvents(cacheDir)
	seen := make(map[string]bool, len(merged))
	for _, e := range merged {
		seen[runtimeKey(e)] = true
	}
	for _, e := range fresh {
		if e.Unit == "" || e.Code == "" || seen[runtimeKey(e)] {
			continue
		}
		seen[runtimeKey(e)] = true
		merged = append(merged, e)
	}
	if len(merged) > defaultRuntimeCap {
		merged = merged[len(merged)-defaultRuntimeCap:]
	}
	b, err := codec.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(StoreDir(cacheDir), 0o755); err != nil {
		return err
	}
	return file.WriteFileAtomic(runtimeRecordsPath(cacheDir), b, 0o644)
}

func runtimeKey(e types.DiagnosticEvent) string { return e.Unit + "\x00" + string(e.Code) }
