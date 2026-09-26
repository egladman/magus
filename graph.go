package magus

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// ComposeOption configures a ComposeGraph call.
type ComposeOption func(*compose)

// compose is the accumulated state of a ComposeGraph call.
type compose struct {
	Graph       *types.Graph
	History     *forecast.History
	Target      string
	Upstream    bool
	SpellFilter string
	RootFilter  []string
}

// WithGraphInput enables blast-radius enrichment.
func WithGraphInput(g *types.Graph) ComposeOption {
	return func(c *compose) { c.Graph = g }
}

// WithUpstream switches graph direction to upstream (dependents instead of dependencies).
func WithUpstream() ComposeOption {
	return func(c *compose) { c.Upstream = true }
}

// WithComposeSpell limits the graph to projects that use the named spell.
func WithComposeSpell(name string) ComposeOption {
	return func(c *compose) { c.SpellFilter = name }
}

// WithComposeRoots restricts the graph to the listed project paths.
func WithComposeRoots(paths ...string) ComposeOption {
	return func(c *compose) { c.RootFilter = append(c.RootFilter, paths...) }
}

// WithGraphHistory enables per-node DurationMs prediction in ComposeGraph using
// adaptive CI history for the given target (typically "ci" or "test").
func WithGraphHistory(h *forecast.History, target string) ComposeOption {
	return func(c *compose) { c.History = h; c.Target = target }
}

// ComposeGraph assembles the structured graph view. Edges to unknown projects are dropped.
func ComposeGraph(ws types.WorkspaceRepository, opts ...ComposeOption) types.GraphOutput {
	cfg := &compose{}
	for _, o := range opts {
		o(cfg)
	}

	upstream := cfg.Upstream
	spell := cfg.SpellFilter
	rootFilter := cfg.RootFilter

	out := types.GraphOutput{Direction: "downstream"}
	if upstream {
		out.Direction = "upstream"
	}
	if spell != "" {
		out.SpellName = spell
	}

	rootSet := map[string]struct{}{}
	for _, r := range rootFilter {
		rootSet[r] = struct{}{}
	}

	all := ws.All()
	downstream := make(map[string][]string, len(all))
	for _, p := range all {
		var kids []string
		for _, dep := range p.DependsOn {
			if ws.Get(dep) != nil {
				kids = append(kids, dep)
			}
		}
		slices.Sort(kids)
		downstream[p.Path] = kids
	}

	var blastRadius map[string]int
	if cfg.Graph != nil {
		blastRadius = cfg.Graph.BlastRadius()
	}

	for _, p := range all {
		if spell != "" && !slices.Contains(p.Spells, spell) && p.Spell != spell {
			continue
		}
		if len(rootSet) > 0 {
			if _, ok := rootSet[p.Path]; !ok {
				continue
			}
		}

		var kids []string
		if upstream {
			for _, q := range all {
				for _, dep := range downstream[q.Path] {
					if dep == p.Path {
						if spell == "" || q.Spell == spell || slices.Contains(q.Spells, spell) {
							kids = append(kids, q.Path)
						}
						break
					}
				}
			}
		} else {
			for _, dep := range downstream[p.Path] {
				if dep == p.Path {
					continue
				}
				if spell != "" {
					if dp := ws.Get(dep); dp == nil || (dp.Spell != spell && !slices.Contains(dp.Spells, spell)) {
						continue
					}
				}
				kids = append(kids, dep)
			}
		}
		slices.Sort(kids)
		if kids == nil {
			kids = []string{}
		}
		node := types.Node{
			Path:      p.Path,
			Name:      types.ProjectDisplayName(p.Path, p.Name, p.Dir),
			SpellName: p.Spell,
			Children:  kids,
			Dir:       p.Dir,
		}
		if blastRadius != nil {
			node.BlastRadius = blastRadius[p.Path]
		}
		if cfg.History != nil && cfg.Target != "" {
			d := cfg.History.PredictDuration(p.Path, cfg.Target, nil)
			if d > 0 {
				node.DurationMs = d.Milliseconds()
			}
		}
		out.Nodes = append(out.Nodes, node)
	}
	if len(rootFilter) > 0 {
		out.Roots = append(out.Roots, rootFilter...)
	}
	return out
}

// warmGraph is the server's concurrency-safe cache of the workspace knowledge
// graph. It is always fresh, never best-effort: the cache is trusted only while a
// file watcher invalidates it on source changes; without a watcher (a one-shot
// CLI) every Get rebuilds cache-first, identical to the old always-rebuild path.
// Rebuilds are single-flight. Gated on the watcher rather than a TTL so an agent
// never gets a stale answer and learns to distrust the graph.
type warmGraph struct {
	// rebuild builds the graph; its bool is the caller's refresh, which decides whether
	// the SHARDS are rebuilt or read from the cache. It is threaded rather than bound to
	// false because Get's own refresh only invalidates this process's memory: a caller
	// asking for a refresh and getting a rebuild from stale shards is a knob that lies.
	rebuild func(ctx context.Context, refresh bool) (*knowledge.Graph, error)
	log     *slog.Logger

	buildMu sync.Mutex // serializes rebuilds so concurrent misses build once

	mu       sync.RWMutex // guards the fields below
	graph    *knowledge.Graph
	valid    bool   // graph is populated AND known-fresh (a watcher is invalidating it)
	watching bool   // a watcher is active; only then is the cache trusted
	gen      uint64 // bumped on every invalidation, to catch a change landing mid-rebuild
}

func newWarmGraph(rebuild func(context.Context, bool) (*knowledge.Graph, error), log *slog.Logger) *warmGraph {
	if log == nil {
		log = slog.Default()
	}
	return &warmGraph{rebuild: rebuild, log: log}
}

// Get returns the workspace graph. When a watcher is active and the cache is
// fresh, it returns the warm graph without touching the filesystem. Otherwise it
// rebuilds cache-first under a single-flight lock. refresh forces a rebuild.
func (w *warmGraph) Get(ctx context.Context, refresh bool) (*knowledge.Graph, error) {
	if !refresh {
		if g := w.cached(); g != nil {
			return g, nil
		}
	}

	// Miss (or refresh): rebuild under buildMu so concurrent misses coalesce.
	w.buildMu.Lock()
	defer w.buildMu.Unlock()
	if !refresh {
		if g := w.cached(); g != nil {
			return g, nil // another goroutine rebuilt while we waited for the lock
		}
	}

	// Capture the generation before building. buildMu guarantees no other builder
	// runs, so gen changes only if the watcher invalidates DURING our build.
	w.mu.RLock()
	startGen, watching := w.gen, w.watching
	w.mu.RUnlock()

	g, err := w.rebuild(ctx, refresh)
	if err != nil {
		return nil, err
	}

	w.mu.Lock()
	if watching && w.gen == startGen {
		// No change landed mid-build and a watcher can invalidate: trust the cache.
		w.graph, w.valid = g, true
	} else {
		// Either no watcher (never trust) or a change landed while we built (the
		// graph we just produced may already miss it): leave the cache untrusted so
		// the next Get rebuilds. This caller still gets the graph it asked for.
		w.graph, w.valid = nil, false
	}
	w.mu.Unlock()
	return g, nil
}

// Healthy reports the warm graph's watcher state for the server's /readyz readiness
// surface: watching is true once a file watcher is invalidating the cache on source
// changes, valid is true when the cache currently holds a fresh graph (so the next Get
// answers from memory instead of rebuilding). Guarded by the same mutex as Get/cached.
func (w *warmGraph) Healthy() (watching, valid bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.watching, w.valid
}

// cached returns the warm graph if it is populated and known-fresh, else nil.
func (w *warmGraph) cached() *knowledge.Graph {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.watching && w.valid {
		return w.graph
	}
	return nil
}

// invalidate drops the cached graph so the next Get rebuilds, and bumps the
// generation so a rebuild in flight does not publish a graph that missed this
// change. Called by the watcher on a graph-relevant source change.
func (w *warmGraph) invalidate() {
	w.mu.Lock()
	w.valid, w.graph = false, nil
	w.gen++
	w.mu.Unlock()
}

// watch starts a file watcher over root that invalidates the cache on any
// graph-relevant change and marks the cache trustworthy. It returns a stop
// function. If the watcher cannot start, the cache stays untrusted (every Get
// rebuilds cache-first) and the error is returned. Call before serving requests
// so a change between the first build and the watcher's start cannot be missed.
func (w *warmGraph) watch(ctx context.Context, root string) (func(), error) {
	wctx, cancel := context.WithCancel(ctx)
	// BuiltinIgnore is essential, not cosmetic: it skips the magus cache dir, so
	// the graph build's own shard writes under <cache>/knowledge never trip the
	// watcher (which would invalidate-rebuild-invalidate forever). It also skips
	// VCS metadata and editor temporaries.
	watcher, err := watch.New(wctx,
		watch.WithRoot(root),
		watch.WithIgnore(watch.BuiltinIgnore),
	)
	if err != nil {
		cancel()
		return nil, err
	}

	w.mu.Lock()
	w.watching = true
	w.mu.Unlock()

	go func() {
		defer watcher.Close()
		for {
			select {
			case <-wctx.Done():
				w.stopWatching()
				return
			case batch, ok := <-watcher.Events():
				if !ok {
					// The watcher died; stop trusting the cache and fall back to
					// always-rebuild so we never serve a graph nothing invalidates.
					w.log.WarnContext(ctx, "magus: knowledge-graph watcher stopped; falling back to a cache-first rebuild per query")
					w.stopWatching()
					return
				}
				if graphRelevant(batch.Paths) {
					w.invalidate()
				}
			}
		}
	}()

	return cancel, nil
}

// stopWatching marks the cache untrusted and drops it, so Get reverts to
// always-rebuild once the watcher is gone.
func (w *warmGraph) stopWatching() {
	w.mu.Lock()
	w.watching, w.valid, w.graph = false, false, nil
	w.mu.Unlock()
}

// KnowledgeGraphHealthy reports the server's warm-knowledge-graph watcher state, for the
// /readyz readiness surface's "knowledge_graph" component. It goes through
// warmKnowledgeGraph (the same lazily-created holder KnowledgeGraph reads), so calling it
// before WatchKnowledgeGraph has ever run reports watching=false rather than panicking on
// a nil holder, and calling it after does not create a second holder (sync.Once).
func (m *Magus) KnowledgeGraphHealthy() (watching, valid bool) {
	return m.warmKnowledgeGraph().Healthy()
}

// graphRelevant reports whether any changed path feeds the knowledge graph: a buzz
// source, a markdown doc, or a magus config file. Other edits (Go, assets) do not
// change the graph, so they must not invalidate it: over-invalidating would pay a
// needless re-parse on the next query.
func graphRelevant(paths []string) bool {
	for _, p := range paths {
		if strings.HasSuffix(p, ".buzz") || strings.HasSuffix(p, ".md") {
			return true
		}
		switch filepath.Base(p) {
		case "magus.yaml", "magus.yml", "magusfiles":
			return true
		}
	}
	return false
}
