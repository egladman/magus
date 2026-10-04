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
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/readlog"
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
	// Inputs is the stamp each class's shards were last assembled under (see Stamps). A
	// class with no entry matches no stamp, so a store written before stamps existed, or
	// by a Sync that had none, is reassembled on its next build.
	Inputs map[ShardClass]string `json:"inputs,omitempty"`
	// FastStamps is the cheap stamp each class's shards were last synced under (see
	// BuildOptions.FastStamps): Inputs minus the evaluated workspace model. A read whose
	// fast stamp matches is answered from the store without evaluating the workspace; one
	// whose does not falls back to Inputs. Absent for a class synced before fast stamps
	// existed, which Ensure repairs on the first read the full stamp settles.
	FastStamps map[ShardClass]string `json:"fast_stamps,omitempty"`
	// Reads is what the evaluation the shards were last synced from read beyond the tree
	// (see Inputs.Reads): the environment variables and files its magusfile top levels
	// consulted. The next read's fast domain stamp folds their current values, so an
	// evaluation that depends on the environment is redone when the environment moves.
	Reads readlog.Reads `json:"reads,omitempty"`
	// ReadsKnown distinguishes an evaluation that read nothing from a manifest that never
	// recorded its reads, which the encoding of an empty record cannot. A fast domain
	// stamp computed without known reads matches nothing recorded with them.
	ReadsKnown bool `json:"reads_known,omitempty"`
	// Indexes are the symbol indexes the workspace declared when its shards were last
	// synced from an evaluated workspace (see Inputs.Indexes), each with the freshness
	// verdict that evaluation reached and the identity of the index file it judged. They
	// are a function of the same inputs as FastStamps[ClassDomain], so a read whose domain
	// fast stamp matches may use them in place of evaluating the workspace; one whose
	// does not must not.
	Indexes []SymbolIndexDeclaration `json:"indexes,omitempty"`
	// IndexesKnown distinguishes a workspace that declares no index from a manifest that
	// never recorded them, which the encoding of an empty list cannot.
	IndexesKnown bool `json:"indexes_known,omitempty"`
	// Extra is, per class, the workspace files its assembly read that the tree walk does
	// not cover, so the caller can fold them into that class's next stamp.
	Extra map[ShardClass][]string `json:"extra,omitempty"`
	// Routing is the symbolShardsKey the routing file was last written for, so a sync whose
	// symbol shards came out unchanged leaves that multi-megabyte file alone.
	Routing string `json:"routing,omitempty"`
}

type shardMeta struct {
	Fingerprint string `json:"fingerprint"`
	NodeCount   int    `json:"node_count"`
	EdgeCount   int    `json:"edge_count"`
}

// SymbolIndexDeclaration is one symbol index a workspace declares: the project it covers,
// where that project's directory is, the language its symbols are written in, and the
// absolute path of the index file. The resolution is the caller's (it reads the evaluated
// projects and spells); the store only records and returns it.
//
// Freshness is the verdict the evaluation reached about the index file at Path (one of
// types.SymbolIndexFreshness, "" when none was reached), and Size and ModTime identify the
// file it judged. The verdict is a function of the sources, which the domain fast stamp
// covers, and of that file: a later read may reuse it while both are unchanged, and must
// re-judge once the file differs.
type SymbolIndexDeclaration struct {
	Project   string `json:"project"`
	Dir       string `json:"dir"`
	Language  string `json:"language,omitempty"`
	Path      string `json:"path"`
	Freshness string `json:"freshness,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Size      int64  `json:"size,omitempty"`
	ModTime   int64  `json:"mod_time,omitempty"`
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

// NewStore returns a store rooted at <cacheDir>/knowledge. immutable is set when
// cache.write.enabled is false (Sync writes nothing, warns if stale). maxBytes
// soft-caps the shards dir (0 = unlimited); remote optionally backs shards (nil = local).
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
//
// A writing Sync holds the store's cross-process lock throughout. Every query in every
// process on the checkout builds the graph, and two unlocked Syncs interleave: one's
// shard lands under the other's manifest, whose fingerprint then matches every later
// build, so the stale shard is never rewritten; or one prunes a shard the other's
// manifest still names.
func (s *Store) Sync(ctx context.Context, shards []Shard, fps map[string]string, refresh bool) (*Graph, error) {
	if err := s.syncClasses(ctx, shards, fps, syncPlan{refresh: refresh}); err != nil {
		return nil, err
	}
	return mergeShards(shards), nil
}

// syncPlan says which part of the store a sync owns and what to record for it.
type syncPlan struct {
	// classes are the classes shards carries in full; nil means every class. Shards of any
	// other class keep their manifest entries and files untouched, which is what lets a
	// build reassemble one class without the inputs of the rest.
	classes    []ShardClass
	stamps     Stamps
	fastStamps Stamps                   // see manifest.FastStamps; nil records none
	indexes    []SymbolIndexDeclaration // see manifest.Indexes; nil keeps the recorded ones
	reads      *readlog.Reads           // see manifest.Reads; nil keeps the recorded ones
	extra      map[ShardClass][]string
	refresh    bool
}

func (p syncPlan) covers(c ShardClass) bool { return p.classes == nil || slices.Contains(p.classes, c) }

// syncClasses reconciles the shards of the plan's classes against the store, under the
// cross-process lock unless the store is immutable.
func (s *Store) syncClasses(ctx context.Context, shards []Shard, fps map[string]string, plan syncPlan) error {
	if s.immutable {
		return s.sync(ctx, shards, fps, plan)
	}
	return file.WithLock(ctx, filepath.Join(s.dir, ".sync.lock"), syncLockWait, func() error {
		return s.sync(ctx, shards, fps, plan)
	})
}

// mergeShards merges the default shards into a fresh graph in shard-name order, the
// order Load uses, so a graph answered from the store and one assembled in memory are
// the same graph: AddNode and AddEdge are first-writer-wins on conflict, so merge
// order is content. Lazily loaded shards stay out; a caller that wants them merges
// them itself.
func mergeShards(shards []Shard) *Graph {
	picked := make([]Shard, 0, len(shards))
	for _, sh := range shards {
		if !isLazyShard(sh.Name) {
			picked = append(picked, sh)
		}
	}
	slices.SortFunc(picked, func(a, b Shard) int { return strings.Compare(a.Name, b.Name) })
	// optimization: size the node and edge maps from the shards' own counts. Merge
	// inserts every node and edge, so growing from empty rehashes each map about log2(n)
	// times; the counts are known before the first insert. Shards share few nodes (an
	// op or spell node two shards both declare), so the sum is a tight upper bound.
	//
	//	measured: BenchmarkMergeShards presized vs grown (means, n=6, 2000-project
	//	          fixture): 92.2 ms -> 76.7 ms (-17%), 53.3 MB -> 33.6 MB/op (-37%).
	nodes, edges := 0, 0
	for _, sh := range picked {
		nodes += len(sh.Nodes)
		edges += len(sh.Edges)
	}
	g := newGraphSized(nodes, edges)
	for _, sh := range picked {
		g.Merge(sh.Nodes, sh.Edges)
	}
	return g
}

// syncLockWait bounds the wait for another Sync. A cold build of a large workspace
// writes thousands of shards and has been measured near 10s, so file.LockWait would
// give up on a holder that is only busy.
const syncLockWait = 2 * time.Minute

func (s *Store) sync(ctx context.Context, shards []Shard, fps map[string]string, plan syncPlan) error {
	old := s.readManifestOrNil()
	newMan := manifest{
		SchemaVersion: types.KnowledgeSchemaVersion,
		Shards:        map[string]shardMeta{},
		Inputs:        map[ShardClass]string{},
		FastStamps:    map[ShardClass]string{},
		Extra:         map[ShardClass][]string{},
	}
	if old != nil {
		newMan.Routing = old.Routing
		for name, meta := range old.Shards {
			if !plan.covers(shardClass(name)) {
				newMan.Shards[name] = meta
			}
		}
		for c, stamp := range old.Inputs {
			if !plan.covers(c) {
				newMan.Inputs[c] = stamp
			}
		}
		for c, stamp := range old.FastStamps {
			if !plan.covers(c) {
				newMan.FastStamps[c] = stamp
			}
		}
		newMan.Indexes, newMan.IndexesKnown = old.Indexes, old.IndexesKnown
		newMan.Reads, newMan.ReadsKnown = old.Reads, old.ReadsKnown
		for c, paths := range old.Extra {
			if !plan.covers(c) {
				newMan.Extra[c] = paths
			}
		}
	}
	if plan.indexes != nil {
		newMan.Indexes, newMan.IndexesKnown = plan.indexes, true
	}
	if plan.reads != nil {
		newMan.Reads, newMan.ReadsKnown = *plan.reads, true
	}
	prev := old
	if plan.refresh {
		prev = nil
	}

	present := make(map[string]bool, len(shards))
	changed := false
	var toWrite []shardWrite
	for _, sh := range shards {
		if err := ctx.Err(); err != nil {
			return err
		}
		present[sh.Name] = true
		fp := fps[sh.Name]
		if sh.Dropped > 0 {
			s.log.DebugContext(ctx, "knowledge: shard inputs resolved to no node",
				slog.String("shard", sh.Name), slog.Int("dropped", sh.Dropped), slog.Int("nodes", len(sh.Nodes)))
		}
		newMan.Shards[sh.Name] = shardMeta{Fingerprint: fp, NodeCount: len(sh.Nodes), EdgeCount: len(sh.Edges)}

		p, ok := prev.shard(sh.Name)
		unchanged := ok && p.Fingerprint == fp && s.shardExists(sh.Name)
		if unchanged {
			continue
		}
		changed = true
		if s.immutable {
			continue
		}
		toWrite = append(toWrite, shardWrite{shard: sh, fp: fp})
	}
	for _, c := range AllClasses {
		if !plan.covers(c) {
			continue
		}
		if stamp := plan.stamps[c]; stamp != "" {
			newMan.Inputs[c] = stamp
		}
		if stamp := plan.fastStamps[c]; stamp != "" {
			newMan.FastStamps[c] = stamp
		}
		if paths := plan.extra[c]; len(paths) > 0 {
			newMan.Extra[c] = paths
		}
	}
	// A fast stamp for a class this plan does not cover belongs to one Ensure settled fresh
	// by its full stamp in the same read (a wanted class is either rebuilt here or fresh),
	// so it is current and worth recording: a store written before fast stamps existed
	// would otherwise keep paying the full stamps for that class until a read rebuilt
	// nothing at all (see recordFastStamps).
	for c, stamp := range plan.fastStamps {
		if stamp != "" && !plan.covers(c) && newMan.Inputs[c] != "" {
			newMan.FastStamps[c] = stamp
		}
	}
	var pruned []string
	if old != nil {
		for name := range old.Shards {
			if plan.covers(shardClass(name)) && !present[name] {
				pruned = append(pruned, name)
			}
		}
	}

	if s.immutable {
		// Warn only when a prior store exists and diverges: a first-ever run
		// under immutable mode is uninitialized, not stale.
		if old != nil && (changed || len(pruned) > 0) {
			s.log.WarnContext(ctx, "magus: knowledge graph is stale but cache.write.enabled is false; serving a freshly assembled in-memory graph without persisting")
		}
		return nil
	}

	if err := s.writeShards(ctx, toWrite); err != nil {
		return err
	}
	s.recordPathIDs(ctx, shards, fps, newMan)

	// Refresh the derived symbol xref routing index (best-effort: a failure just means
	// `magus refs` falls back to loading all symbol shards, never a wrong result; the index
	// is bound to newMan so a stale one is detected and ignored on read). Only a sync that
	// carries the symbol shards can build it, and only one whose shards came out different
	// needs to: the index is a pure function of them. Written before the manifest, so a
	// crash between the two leaves an index bound to a manifest that never landed, which
	// reads as stale.
	if plan.covers(ClassSymbols) {
		key := symbolShardsKey(&newMan)
		if key != newMan.Routing || (key != "" && (!fileExists(s.routingPath()) || !fileExists(s.namesPath()))) {
			newMan.Routing = ""
			if err := s.writeXref(shards, newMan); err != nil {
				s.log.DebugContext(ctx, "knowledge: symbol xref routing write failed", slog.String("error", err.Error()))
			} else {
				newMan.Routing = key
			}
		}
	}

	// optimization: on a no-op rebuild (nothing changed, nothing to prune, no new stamp)
	// the on-disk manifest already matches, so skip rewriting it, which also keeps the
	// manifest's mtime stable.
	//   measured: folded into the BenchmarkBuildNoop delta above; removes the
	//             one guaranteed write from the otherwise write-free hot path.
	//   trade-off: none; the manifest is only skipped when it would be identical.
	if !changed && len(pruned) == 0 && maps.Equal(old.inputs(), newMan.Inputs) && maps.Equal(old.fastStamps(), newMan.FastStamps) &&
		slices.Equal(old.indexes(), newMan.Indexes) && old.indexesKnown() == newMan.IndexesKnown &&
		readsEqual(old.reads(), newMan.Reads) && old.readsKnown() == newMan.ReadsKnown &&
		maps.EqualFunc(old.extra(), newMan.Extra, slices.Equal) && old.routing() == newMan.Routing {
		return nil
	}

	// Write the manifest before pruning the shards it no longer references: the
	// manifest is the index, so a crash after it lands leaves only orphan shard
	// files (ignored by Load) rather than a manifest pointing at a deleted shard.
	if err := s.writeManifest(newMan); err != nil {
		return err
	}
	for _, name := range pruned {
		if err := s.removeShard(name); err != nil {
			return err
		}
	}
	// Enforce the soft size cap last, once every current shard is on disk, so the
	// newest shards survive and only cold ones are evicted.
	s.pruneToSize()
	return nil
}

// staleClasses returns the members of want whose recorded stamp is missing or differs
// from stamps. refresh, or a store with no readable manifest, makes every one stale.
func staleClasses(man *manifest, stamps Stamps, want []ShardClass, refresh bool) []ShardClass {
	var stale []ShardClass
	for _, c := range want {
		cur := stamps[c]
		if refresh || man == nil || cur == "" || man.Inputs[c] != cur {
			stale = append(stale, c)
		}
	}
	return stale
}

// recordFastStamps writes the fast stamp of each class in classes whose recorded one is missing or
// differs, and nothing else: no shard moves, so the full stamps and the shard entries stay
// as man has them. Called when the full stamps settled a class the fast ones could not,
// which is a store written before fast stamps existed, so that one read pays for the full
// stamps and the next does not. A class with no fast stamp records nothing: a stamp the
// caller could not compute must never be recorded as a match. Immutable stores, and a
// manifest another process rewrote since man was read, record nothing either.
func (s *Store) recordFastStamps(ctx context.Context, man *manifest, rec fastStampRecord) error {
	if s.immutable || man == nil {
		return nil
	}
	var missing []ShardClass
	for _, c := range rec.classes {
		if cur := rec.stamps[c]; cur != "" && man.FastStamps[c] != cur {
			missing = append(missing, c)
		}
	}
	recordIndexes := rec.indexes != nil && !man.IndexesKnown
	recordReads := rec.reads != nil && !man.ReadsKnown
	if len(missing) == 0 && !recordIndexes && !recordReads {
		return nil
	}
	return file.WithLock(ctx, filepath.Join(s.dir, ".sync.lock"), syncLockWait, func() error {
		cur := s.readManifestOrNil()
		if cur == nil || !maps.Equal(cur.inputs(), man.inputs()) || !maps.Equal(cur.Shards, man.Shards) {
			// The store moved under this read; whoever moved it recorded its own stamps.
			return nil
		}
		if cur.FastStamps == nil {
			cur.FastStamps = map[ShardClass]string{}
		}
		for _, c := range missing {
			cur.FastStamps[c] = rec.stamps[c]
		}
		if recordIndexes {
			cur.Indexes, cur.IndexesKnown = rec.indexes, true
		}
		if recordReads {
			cur.Reads, cur.ReadsKnown = *rec.reads, true
		}
		return s.writeManifest(*cur)
	})
}

// fastStampRecord is what recordFastStamps writes onto a manifest whose shards stay as
// they are: the fast stamps of the classes a read settled by their full stamps, and, when
// the caller resolved the evaluation anyway, what it read and the indexes it declared. A
// nil indexes or reads records none.
type fastStampRecord struct {
	stamps  Stamps
	classes []ShardClass
	indexes []SymbolIndexDeclaration
	reads   *readlog.Reads
}

// readsEqual reports whether two evaluation reads name the same inputs.
func readsEqual(a, b readlog.Reads) bool {
	return a.EnvAll == b.EnvAll && slices.Equal(a.Files, b.Files) && slices.Equal(a.Env, b.Env)
}

// ExtraInputs returns the files the last assembly of class read outside the tree walk.
func (s *Store) ExtraInputs(class ShardClass) []string {
	man := s.readManifestOrNil()
	if man == nil {
		return nil
	}
	return man.Extra[class]
}

// readClassShards reads every stored shard of the given classes, checking each file's
// fingerprint against the manifest's. A class with any shard missing, unreadable or
// rewritten since the manifest was read is returned in bad rather than read in part:
// a class is answered whole from the store or reassembled.
func (s *Store) readClassShards(ctx context.Context, man *manifest, classes []ShardClass) ([]Shard, []ShardClass, error) {
	if man == nil || len(classes) == 0 {
		return nil, classes, nil
	}
	var names []string
	for name := range man.Shards {
		if slices.Contains(classes, shardClass(name)) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	out := make([]Shard, len(names))
	failed := make([]bool, len(names))
	eg, egctx := errgroup.WithContext(ctx)
	eg.SetLimit(runtime.GOMAXPROCS(0))
	for i, name := range names {
		eg.Go(func() error {
			if err := egctx.Err(); err != nil {
				return err
			}
			sf, err := s.readVerifiedShard(egctx, man, name)
			if err != nil {
				s.log.DebugContext(egctx, "knowledge: stored shard unusable, reassembling its class",
					slog.String("shard", name), slog.String("error", err.Error()))
				failed[i] = true
				return nil
			}
			out[i] = Shard{Name: name, Nodes: sf.Nodes, Edges: sf.Edges}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, nil, err
	}
	var bad []ShardClass
	for i, name := range names {
		if c := shardClass(name); failed[i] && !slices.Contains(bad, c) {
			bad = append(bad, c)
		}
	}
	kept := out[:0]
	for i, sh := range out {
		if !failed[i] && !slices.Contains(bad, shardClass(names[i])) {
			kept = append(kept, sh)
		}
	}
	return kept, bad, nil
}

// readVerifiedShard reads one shard, restoring an evicted file from the remote first, and
// refuses a file whose fingerprint is not the manifest's: another process may have
// rewritten it after this one read the manifest, and mixing the two would answer with a
// graph no single build produced.
func (s *Store) readVerifiedShard(ctx context.Context, man *manifest, name string) (shardFile, error) {
	want := man.Shards[name].Fingerprint
	sf, err := s.readShard(name)
	if err != nil {
		if s.restoreShard(ctx, name, want) == nil {
			sf, err = s.readShard(name)
		}
		if err != nil {
			return shardFile{}, err
		}
	}
	if sf.Fingerprint != want {
		return shardFile{}, fmt.Errorf("knowledge: shard %q holds fingerprint %.12s, the manifest names %.12s", name, sf.Fingerprint, want)
	}
	return sf, nil
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
	// Sorted, for the same reason MergeSymbolShards sorts and the Sync path merges a
	// slice: AddNode and AddEdge are both FIRST-WRITER-WINS, so which shard supplies a
	// node's Source (or which of two equally-confident edges survives) is decided by
	// merge order. Ranging man.Shards directly took Go's randomized map order, which
	// made the merged graph differ run to run, invisibly, because the output is sorted
	// by ID afterward, so only the provenance fields moved and the node and edge counts
	// never budged. That is what broke `magus run generate`'s drift gate: the committed
	// gen/*.json and a freshly built one disagreed on "source" lines alone.
	names := make([]string, 0, len(man.Shards))
	for name := range man.Shards {
		if isLazyShard(name) {
			continue // lazily loaded via MergeSymbolShards, not part of the default graph
		}
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := s.readMergeShard(ctx, g, man, name); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// readMergeShard reads shard name and merges it into g, restoring an LRU-evicted file
// from the remote by fingerprint first, and returns the fingerprint the file held. Shared by
// Load and the symbol-shard loaders.
func (s *Store) readMergeShard(ctx context.Context, g *Graph, man *manifest, name string) (string, error) {
	return s.readMergeShardKeeping(ctx, g, man, name, true)
}

func (s *Store) readMergeShardKeeping(ctx context.Context, g *Graph, man *manifest, name string, keep bool) (string, error) {
	sf, err := s.readShardKeeping(name, keep)
	if err != nil {
		// The file may have been LRU-evicted while its manifest entry stayed.
		if s.restoreShard(ctx, name, man.Shards[name].Fingerprint) == nil {
			sf, err = s.readShardKeeping(name, keep)
		}
		if err != nil {
			return "", fmt.Errorf("knowledge: load shard %q: %w", name, err)
		}
	}
	g.Merge(sf.Nodes, sf.Edges)
	return sf.Fingerprint, nil
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
	key := mergedKey(g, man)
	if key != "" {
		if cached := readCache.mergedFor(s.dir, key); cached != nil {
			g.adopt(cached)
			return nil
		}
	}
	exact, err := s.mergeSymbolShards(ctx, g, man, key == "")
	if err != nil {
		return err
	}
	if key != "" && exact {
		readCache.putMerged(s.dir, key, g.share(), s.lazyShardsSize(man))
	}
	return nil
}

// mergeSymbolShards merges what MergeSymbolShards does into g, keeping the decoded symbol
// shards in the read cache when keep is set. exact reports that every file merged held the
// fingerprint man names, so the result is the graph man describes and not one mixed with
// another process's rewrite: only that graph may be cached under man's key.
func (s *Store) mergeSymbolShards(ctx context.Context, g *Graph, man *manifest, keep bool) (exact bool, err error) {
	// Merge in sorted shard order: if a symbol ID appears in two projects' shards
	// with a different label/source, AddNode is first-writer-wins, so a stable order
	// keeps the merged node deterministic (the domain Sync path merges a sorted slice).
	names := make([]string, 0, len(man.Shards))
	for name := range man.Shards {
		if isSymbolsShard(name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	exact = true
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		fp, err := s.readMergeShardKeeping(ctx, g, man, name, keep)
		if err != nil {
			return false, err
		}
		exact = exact && fp == man.Shards[name].Fingerprint
	}
	exact = s.mergeOverlayShard(ctx, g, man, coverageShardName) && exact
	exact = s.mergeOverlayShard(ctx, g, man, sessionShardName) && exact
	return exact, nil
}

// lazyShardsSize sums the on-disk size of the lazy shards man lists, the read cache's
// measure of a symbol-merged graph.
func (s *Store) lazyShardsSize(man *manifest) int64 {
	var total int64
	for name := range man.Shards {
		if !isLazyShard(name) {
			continue
		}
		if info, err := os.Stat(s.shardPath(name)); err == nil {
			total += info.Size()
		}
	}
	return total
}

// SymbolIndexDigest identifies the @symbols shards MergeSymbolShards loads: a hex SHA-256
// over each shard's fingerprint, keyed by project (and directory, for a split project) in
// sorted order, so it moves exactly when a symbol-reading answer can. No store, or no symbol
// shard, is Indexed false with no error. The caller reports gaps, since it knows the
// declarations.
func (s *Store) SymbolIndexDigest() (types.SymbolIndexDigest, error) {
	out := types.SymbolIndexDigest{Projects: []string{}}
	man := s.readManifestOrNil()
	if man == nil {
		return out, nil
	}
	fps := map[string]string{}
	for name, meta := range man.Shards {
		if !isSymbolsShard(name) {
			continue
		}
		// A blank fingerprint would hash as a constant and pin the digest while the
		// shard's content moved under it.
		if meta.Fingerprint == "" {
			return types.SymbolIndexDigest{}, fmt.Errorf("knowledge: symbol shard %q has no fingerprint; rebuild with `magus graph build`", name)
		}
		fps[symbolsShardKey(name)] = meta.Fingerprint
		if p := symbolsShardProject(name); !slices.Contains(out.Projects, p) {
			out.Projects = append(out.Projects, p)
		}
	}
	if len(out.Projects) == 0 {
		return out, nil
	}
	slices.Sort(out.Projects)
	h := sha256.New()
	for _, k := range slices.Sorted(maps.Keys(fps)) {
		fmt.Fprintf(h, "%s\x00%s\n", k, fps[k])
	}
	out.Digest = hex.EncodeToString(h.Sum(nil))
	out.Indexed = true
	return out, nil
}

// isLazyShard reports whether a shard is persisted but held out of the default graph,
// loaded only when a query reaches for it. One predicate rather than a disjunction at
// each site, for the reason isMachineLocalShard gives: the exclusion must be added in one place
// or a new lazy shard leaks into the default graph through whichever site was missed.
//
// The @symbols shards are lazy for SCALE (they can dwarf the domain graph); the overlays
// are lazy because the nodes they annotate are the symbol shards' own, so loading them
// eagerly would put bare attr-only nodes in the default graph.
func isLazyShard(name string) bool {
	return isSymbolsShard(name) || isCoverageShard(name) || isSessionShard(name)
}

// mergeOverlayShard folds one observed overlay into g if the manifest lists it. The
// overlays annotate the file/symbol nodes the symbol shards define, so they merge on the
// symbol-load path and never in the default graph. Best-effort: a missing or unreadable
// overlay leaves its attrs absent rather than failing the load, so a workspace that never
// ran `magus run coverage` or `magus session load` behaves exactly as before. It reports
// false only when man lists the overlay and it could not be merged.
func (s *Store) mergeOverlayShard(ctx context.Context, g *Graph, man *manifest, name string) bool {
	if _, ok := man.shard(name); !ok {
		return true
	}
	sf, err := s.readVerifiedShard(ctx, man, name)
	if err != nil {
		s.log.DebugContext(ctx, "knowledge: overlay merge failed",
			slog.String("shard", name), slog.String("error", err.Error()))
		return false
	}
	mergeOverlay(g, Shard{Name: name, Nodes: sf.Nodes, Edges: sf.Edges})
	return true
}

// mergeOverlay folds an overlay into g. @session lands only on nodes g already holds: a
// symbol read does not rebuild it (see SymbolClasses), so it can name a file that has
// since gone, and an overlay mints no node. A current @session names only nodes the graph
// has, so the filter changes nothing for it.
func mergeOverlay(g *Graph, sh Shard) {
	if !isSessionShard(sh.Name) {
		g.Merge(sh.Nodes, sh.Edges)
		return
	}
	for _, n := range sh.Nodes {
		if _, ok := g.node(n.ID); ok {
			g.AddNode(n)
		}
	}
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

// ProjectPaths returns the project paths recorded in the knowledge manifest,
// sorted, or nil when no readable manifest exists. The manifest is the honest
// cheap source for "which projects exist": its shard keys come from
// ws.ListProjects at graph-build time, so one small ReadFile answers the
// question in a hook where a workspace eval is not allowed.
func ProjectPaths(cacheDir string) []string {
	// Through the constructor, not a bare &Store: a hand-built one leaves log nil, so
	// the first logging line added to readManifestOrNil would panic inside a hook.
	man := NewStore(cacheDir, false, 0, nil, nil).readManifestOrNil()
	if man == nil {
		return nil
	}
	var out []string
	for name := range man.Shards {
		// "@"-prefixed singletons and per-project symbol shards are not projects.
		if strings.HasPrefix(name, "@") || isSymbolsShard(name) {
			continue
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func (s *Store) readManifestOrNil() *manifest {
	b, err := os.ReadFile(s.manifestPath())
	if err != nil {
		return nil
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil // a corrupt manifest is treated as absent; a full rebuild follows
	}
	if m.SchemaVersion != types.KnowledgeSchemaVersion {
		return nil // schema bump invalidates the whole store
	}
	readCache.sweep(s.dir, &m)
	return &m
}

func (s *Store) writeManifest(m manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
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
	nodes, edges := sh.canonicalContent()
	sf := shardFile{
		SchemaVersion: types.KnowledgeSchemaVersion,
		Name:          sh.Name,
		Fingerprint:   fp,
		Nodes:         nodes,
		Edges:         edges,
	}
	// optimization: write the shard compact. The indentation was only for a human reading
	// the file, and every reader goes through json.Unmarshal, which takes either. The
	// content fingerprint hashes fields, not these bytes, and canonicalContent fixes the
	// order, so the file's content and its key are unchanged.
	//
	//	measured: this repo's stored graph (10,487 nodes, 19,504 edges) was 9.5 MB
	//	          indented. BenchmarkStoreLoad p200 (means, n=6): 39.6 -> 35.6 ms (-10%,
	//	          inside the run-to-run spread), 13.5 -> 11.9 MB/op (-12%); p2000 is flat in
	//	          time (358 vs 363 ms) and -11% in B/op.
	//	trade-off: a shard is no longer pleasant to read with `cat`; pipe it through jq.
	b, err := json.Marshal(sf)
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
	if s.remote == nil || isMachineLocalShard(name) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, remotePushTimeout)
	defer cancel()
	if err := s.remote.PutShard(ctx, fp, bytes.NewReader(b)); err != nil {
		s.log.DebugContext(ctx, "knowledge: remote shard push failed", slog.String("shard", name), slog.String("error", err.Error()))
	}
}

// ShardBlob is one persisted shard as a publisher sends it.
type ShardBlob struct {
	Name  string // the shard name, which decides merge order
	Key   string // the content fingerprint, the same key GetShard and PutShard use
	Bytes []byte // the shard file verbatim
}

// StoreExport is the persisted store in publishable form: the routing manifest plus one
// blob per shard it names.
type StoreExport struct {
	Manifest []byte
	Shards   []ShardBlob // sorted by shard name
}

// ReadStoreExport reads the persisted store so a publisher can upload it whole.
//
// Machine-local shards are dropped, for the reason pushShard skips them: personal notes,
// loaded transcripts and run history are not the repository's to share. So is any shard
// whose file is gone (LRU-evicted) or whose fingerprint is blank, because neither can be
// addressed by key. All three are dropped from the returned MANIFEST as well, so every
// shard the published routing names has a blob beside it.
//
// Returns ErrNoStore when no readable manifest exists.
func ReadStoreExport(cacheDir string) (StoreExport, error) {
	s := NewStore(cacheDir, true, 0, nil, nil)
	man := s.readManifestOrNil()
	if man == nil {
		return StoreExport{}, ErrNoStore
	}
	kept := manifest{SchemaVersion: man.SchemaVersion, Shards: map[string]shardMeta{}}
	out := StoreExport{}
	for _, name := range slices.Sorted(maps.Keys(man.Shards)) {
		meta := man.Shards[name]
		if isMachineLocalShard(name) || meta.Fingerprint == "" {
			continue
		}
		b, err := os.ReadFile(s.shardPath(name))
		if err != nil {
			continue
		}
		kept.Shards[name] = meta
		out.Shards = append(out.Shards, ShardBlob{Name: name, Key: meta.Fingerprint, Bytes: b})
	}
	b, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return StoreExport{}, err
	}
	out.Manifest = b
	return out, nil
}

// ShardKeys reads a store manifest and maps each shard name to the content fingerprint a
// remote addresses it by. It is how a reader holding a published manifest decides what to
// ask GetShard for.
//
// A manifest written by a different schema is refused rather than partially read: the
// shard files behind it carry that schema's node and edge shapes.
func ShardKeys(manifestJSON []byte) (map[string]string, error) {
	var m manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return nil, fmt.Errorf("knowledge: decode store manifest: %w", err)
	}
	if m.SchemaVersion != types.KnowledgeSchemaVersion {
		return nil, fmt.Errorf("knowledge: store manifest is schema %d, this binary reads %d", m.SchemaVersion, types.KnowledgeSchemaVersion)
	}
	out := make(map[string]string, len(m.Shards))
	for name, meta := range m.Shards {
		out[name] = meta.Fingerprint
	}
	return out, nil
}

// MergeShardFile merges one persisted shard file's bytes into g, for a reader holding
// shard blobs with no store on disk. Merge is first-writer-wins, so a caller merging
// several shards decides provenance by the order it calls this in; the store's own Load
// sorts by shard name.
func MergeShardFile(g *Graph, b []byte) error {
	var sf shardFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return fmt.Errorf("knowledge: decode shard: %w", err)
	}
	g.Merge(sf.Nodes, sf.Edges)
	return nil
}

// readShard decodes shard name's file, through the read cache. The nodes and edges may be
// shared with other readers, so a caller merges them and never writes into them.
func (s *Store) readShard(name string) (shardFile, error) { return s.readShardKeeping(name, true) }

// readShardKeeping is readShard, leaving a decode out of the read cache when keep is false:
// a full symbol merge is cached whole, and holding its shards too would double the memory.
func (s *Store) readShardKeeping(name string, keep bool) (shardFile, error) {
	path := s.shardPath(name)
	sf, info, hit, err := decodeFile(path, func(b []byte) (shardFile, error) {
		var sf shardFile
		err := json.Unmarshal(b, &sf)
		return sf, err
	})
	if err != nil {
		return shardFile{}, err
	}
	if !hit && keep {
		readCache.putFile(path, info, sf, s.dir, name, sf.Fingerprint)
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
// back: it only needs to be deterministic and unique.
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

// inputs and extra read the manifest's stamp records; the nil receiver has none.
func (m *manifest) inputs() map[ShardClass]string {
	if m == nil {
		return nil
	}
	return m.Inputs
}

func (m *manifest) fastStamps() map[ShardClass]string {
	if m == nil {
		return nil
	}
	return m.FastStamps
}

func (m *manifest) indexes() []SymbolIndexDeclaration {
	if m == nil {
		return nil
	}
	return m.Indexes
}

func (m *manifest) indexesKnown() bool { return m != nil && m.IndexesKnown }

func (m *manifest) reads() readlog.Reads {
	if m == nil {
		return readlog.Reads{}
	}
	return m.Reads
}

func (m *manifest) readsKnown() bool { return m != nil && m.ReadsKnown }

// EvaluationReads returns what the evaluation the store was last synced from read beyond
// the tree, and whether that was ever recorded. The caller folds them into the fast domain
// stamp it computes, so they need no validation of their own: a stamp computed over stale
// reads is a stamp that will not match.
func (s *Store) EvaluationReads() (readlog.Reads, bool) {
	man := s.readManifestOrNil()
	return man.reads(), man.readsKnown()
}

// SymbolIndexDeclarations returns the symbol indexes the workspace declared at its last
// evaluated sync, when fastDomain, the caller's current domain fast stamp, is the one that
// sync recorded: the declarations are a function of the same inputs, so a matching stamp
// means they still hold. Any other stamp, a store that never recorded them, or an empty
// fastDomain answers false, and the caller resolves them from the evaluated workspace as
// it always did. An empty slice with true is a workspace that declares no index.
func (s *Store) SymbolIndexDeclarations(fastDomain string) ([]SymbolIndexDeclaration, bool) {
	man := s.readManifestOrNil()
	if man == nil || fastDomain == "" || man.FastStamps[ClassDomain] != fastDomain || !man.IndexesKnown {
		return nil, false
	}
	return slices.Clone(man.Indexes), true
}

func (m *manifest) extra() map[ShardClass][]string {
	if m == nil {
		return nil
	}
	return m.Extra
}

func (m *manifest) routing() string {
	if m == nil {
		return ""
	}
	return m.Routing
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

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

func pathIDsOf(nodes []types.KnowledgeNode) []string {
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
		next[sh.Name] = pathIDEntry{Fingerprint: fp, IDs: pathIDsOf(sh.Nodes)}
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
		s.log.DebugContext(ctx, "knowledge: path ID index write failed", slog.String("error", err.Error()))
	}
}

// storedPathIDs returns the file and dir node IDs of every stored shard whose class is
// not in skip, from the path ID index where it is current and from the shard file otherwise.
func (s *Store) storedPathIDs(ctx context.Context, man *manifest, skip []ShardClass) (map[string]bool, error) {
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
			return nil, fmt.Errorf("knowledge: read shard %q for the overlay's node set: %w", name, err)
		}
		for _, id := range pathIDsOf(sf.Nodes) {
			out[id] = true
		}
	}
	return out, nil
}

// --- runtime diagnostic records (the @runtime shard's persisted input) ---

// defaultRuntimeCap bounds how many distinct (unit, code) records are retained, so
// run history cannot grow the store without limit. Oldest records drop first.
const defaultRuntimeCap = 5000

// RuntimeRecordsPath is where runtime diagnostic records live: next to the shards,
// but not among them (it is the @runtime shard's input, not an output).
func RuntimeRecordsPath(cacheDir string) string {
	return filepath.Join(StoreDir(cacheDir), "runtime.json")
}

// LoadRuntimeEvents reads the persisted runtime diagnostic records; a missing or
// unreadable file yields no events (runtime enrichment is best-effort).
func LoadRuntimeEvents(cacheDir string) []types.DiagnosticEvent {
	b, err := os.ReadFile(RuntimeRecordsPath(cacheDir))
	if err != nil {
		return nil
	}
	var evs []types.DiagnosticEvent
	if err := json.Unmarshal(b, &evs); err != nil {
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
	b, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(StoreDir(cacheDir), 0o755); err != nil {
		return err
	}
	return file.WriteFileAtomic(RuntimeRecordsPath(cacheDir), b, 0o644)
}

func runtimeKey(e types.DiagnosticEvent) string { return e.Unit + "\x00" + string(e.Code) }
