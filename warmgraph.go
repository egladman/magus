package magus

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/knowledge"
)

// warmGraph is the daemon's concurrency-safe cache of the workspace knowledge
// graph. It is always fresh, never best-effort: the cache is trusted only while a
// file watcher invalidates it on source changes; without a watcher (a one-shot
// CLI) every Get rebuilds cache-first, identical to the old always-rebuild path.
// Rebuilds are single-flight. Gated on the watcher rather than a TTL so an agent
// never gets a stale answer and learns to distrust the graph.
type warmGraph struct {
	rebuild func(ctx context.Context) (*knowledge.Graph, error) // cache-first build (refresh=false)
	log     *slog.Logger

	buildMu sync.Mutex // serializes rebuilds so concurrent misses build once

	mu       sync.RWMutex // guards the fields below
	graph    *knowledge.Graph
	valid    bool   // graph is populated AND known-fresh (a watcher is invalidating it)
	watching bool   // a watcher is active; only then is the cache trusted
	gen      uint64 // bumped on every invalidation, to catch a change landing mid-rebuild
}

func newWarmGraph(rebuild func(context.Context) (*knowledge.Graph, error), log *slog.Logger) *warmGraph {
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

	g, err := w.rebuild(ctx)
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
					w.log.Warn("magus: knowledge-graph watcher stopped; falling back to a cache-first rebuild per query")
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

// graphRelevant reports whether any changed path feeds the knowledge graph: a buzz
// source, a markdown doc, or a magus config file. Other edits (Go, assets) do not
// change the graph, so they must not invalidate it - over-invalidating would pay a
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
