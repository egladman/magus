package knowledge

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"slices"
	"sync"
)

// The read cache keeps what a long-lived process decodes from its knowledge stores: shard
// files, the names sidecar and the routing file, and per store the symbol-merged graph with
// its adjacency built, in place of the symbol shards merged into it. A Store is built per call, so the cache is the process's, not a
// Store's. Every entry is bound to what it was read from, so a read through it returns what
// an uncached read returns:
//   - a decoded file to its identity (the same inode, size and mtime); every store write
//     renames a new file into place, so a rewrite never matches;
//   - a merged graph to the identity of the domain graph it was merged onto, the symbol
//     shards' key and both overlays' fingerprints, all taken from the manifest.
//
// It is off until SetReadCacheLimit turns it on: a one-shot CLI read decodes each file once
// anyway, and would only hold the memory longer.
var readCache = &cacheState{}

// maxMergedGraphs caps the merged graphs kept, one per store, apart from the byte limit.
const maxMergedGraphs = 4

type cacheState struct {
	mu     sync.Mutex
	limit  int64 // bytes; 0 = off
	used   int64
	tick   uint64
	files  map[string]*cachedFile
	merged map[string]*mergedGraph // by store dir
}

type cachedFile struct {
	info  os.FileInfo
	value any
	used  uint64
	// store, name and fingerprint say which manifest entry a shard file holds, so a sweep
	// against that store's manifest can drop it; name is empty for the sidecars.
	store, name, fingerprint string
}

type mergedGraph struct {
	key   string
	graph *Graph
	cost  int64
	used  uint64
}

// SetReadCacheLimit turns the process's knowledge read cache on with a soft cap of limit
// bytes, counted as the on-disk size of what it holds; 0 turns it off and drops every entry.
// The cache only ever returns what an uncached read would, so turning it on changes timing
// and memory, never an answer. Safe for concurrent use.
func SetReadCacheLimit(limit int64) {
	readCache.mu.Lock()
	defer readCache.mu.Unlock()
	readCache.limit = max(limit, 0)
	if readCache.limit == 0 {
		readCache.files, readCache.merged, readCache.used = nil, nil, 0
		return
	}
	readCache.evict()
}

func (c *cacheState) enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limit > 0
}

// decodeFile reads path and decodes it, answering from the cache when the file there is
// the one an earlier decode read; hit says it did. On a miss the caller decides whether to
// keep the value, through putFile with the returned identity. A cached value is shared:
// callers must not mutate it.
func decodeFile[T any](path string, decode func([]byte) (T, error)) (v T, info os.FileInfo, hit bool, err error) {
	var zero T
	f, err := os.Open(path)
	if err != nil {
		return zero, nil, false, err
	}
	defer f.Close()
	// The identity comes from the open descriptor, so it describes the bytes read below even
	// if a writer renames another file into place meanwhile.
	if info, err = f.Stat(); err != nil {
		return zero, nil, false, err
	}
	if cached, ok := readCache.file(path, info); ok {
		if t, ok := cached.(T); ok {
			return t, info, true, nil
		}
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return zero, nil, false, err
	}
	if v, err = decode(b); err != nil {
		return zero, nil, false, err
	}
	return v, info, false, nil
}

func (c *cacheState) file(path string, info os.FileInfo) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.files[path]
	if e == nil || !sameFile(e.info, info) {
		return nil, false
	}
	c.tick++
	e.used = c.tick
	return e.value, true
}

func (c *cacheState) putFile(path string, info os.FileInfo, value any, store, name, fingerprint string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limit == 0 || info.Size() > c.limit {
		return
	}
	if c.files == nil {
		c.files = map[string]*cachedFile{}
	}
	if old := c.files[path]; old != nil {
		c.used -= old.info.Size()
	}
	c.tick++
	c.files[path] = &cachedFile{info: info, value: value, used: c.tick, store: store, name: name, fingerprint: fingerprint}
	c.used += info.Size()
	c.evict()
}

func sameFile(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// sweep drops the shard files cached for store whose manifest entry is gone or names
// another fingerprint: a shard rewritten or pruned is never read again under its old
// identity, so holding it only spends the limit.
func (c *cacheState) sweep(store string, man *manifest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for path, e := range c.files {
		if e.store != store || e.name == "" {
			continue
		}
		if meta, ok := man.shard(e.name); !ok || meta.Fingerprint != e.fingerprint {
			c.used -= e.info.Size()
			delete(c.files, path)
		}
	}
}

// mergedFor returns the merged graph cached for store under key, or nil.
func (c *cacheState) mergedFor(store, key string) *Graph {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.merged[store]
	if e == nil || e.key != key {
		return nil
	}
	c.tick++
	e.used = c.tick
	return e.graph
}

// putMerged keeps g as store's merged graph under key, replacing the one it held. cost is
// the on-disk size of the shards merged into it.
func (c *cacheState) putMerged(store, key string, g *Graph, cost int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limit == 0 || cost > c.limit {
		return
	}
	if c.merged == nil {
		c.merged = map[string]*mergedGraph{}
	}
	if old := c.merged[store]; old != nil {
		c.used -= old.cost
	}
	c.tick++
	c.merged[store] = &mergedGraph{key: key, graph: g, cost: cost, used: c.tick}
	c.used += cost
	c.evict()
}

// evict drops least recently used entries until the cache is within its limit and holds
// at most maxMergedGraphs merged graphs. Callers hold mu.
func (c *cacheState) evict() {
	within := func() bool { return c.used <= c.limit && len(c.merged) <= maxMergedGraphs }
	if within() {
		return
	}
	type victim struct {
		used         uint64
		file, merged string
	}
	var all []victim
	for path, e := range c.files {
		all = append(all, victim{used: e.used, file: path})
	}
	for store, e := range c.merged {
		all = append(all, victim{used: e.used, merged: store})
	}
	slices.SortFunc(all, func(a, b victim) int { return cmp.Compare(a.used, b.used) })
	for _, v := range all {
		if within() {
			return
		}
		if v.file != "" {
			c.used -= c.files[v.file].info.Size()
			delete(c.files, v.file)
			continue
		}
		c.used -= c.merged[v.merged].cost
		delete(c.merged, v.merged)
	}
}

// shardsIdentity names the content mergeShards builds from shards, by their sorted names
// and the fingerprints man records for them, which hash their content. "" when man does not
// fingerprint one of them.
func shardsIdentity(shards []Shard, man *manifest) string {
	names := make([]string, 0, len(shards))
	for _, sh := range shards {
		names = append(names, sh.Name)
	}
	slices.Sort(names)
	h := sha256.New()
	for _, name := range names {
		meta, ok := man.shard(name)
		if !ok || meta.Fingerprint == "" {
			return ""
		}
		_, _ = h.Write([]byte(name))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(meta.Fingerprint))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// mergedKey is the identity of the graph MergeSymbolShards makes of g: g's own, the symbol
// shards' and both overlays'. "" when the cache is off or g's content has no identity.
func mergedKey(g *Graph, man *manifest) string {
	if g.base == "" || man == nil || !readCache.enabled() {
		return ""
	}
	key := g.base + "\x00" + symbolShardsKey(man)
	for _, name := range []string{coverageShardName, sessionShardName} {
		meta, _ := man.shard(name)
		key += "\x00" + meta.Fingerprint
	}
	return key
}
