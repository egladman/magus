package evidence

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/workspace"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// Graph is one command's knowledge graph, loaded on first use and handed to every later
// caller, or the same nil when the load failed.
//
// A read that must not build (rebuild false, which is `describe job`) asks first whether a
// stale symbol index could hold a change to the row's write paths. While one could, it
// loads nothing: bringing the graph up to date is a build of seconds, and the symbol postconditions
// graded against it would answer from an index that misses that change. They print as not
// graded, beside the stale-index line that names the rebuild. An index whose changed files
// all fall outside the write paths does not stop the read: the files the row writes are as
// the index saw them. An index that names no changed files, or only some of them, stops it
// as before. With nothing stopping it, it reads the graph as built, so it and
// `job wait` grade alike. A deleted source names no file, so a deletion inside the write
// paths does not stop the read.
//
// One Graph serves one row: the first read that names write paths scopes every later one.
type Graph struct {
	rebuild bool
	// workspace opens the workspace this graph reads.
	workspace func(context.Context) (*magus.Magus, error)
	stale     func(context.Context) []types.KnowledgeStaleIndex
	build     func(context.Context) (*knowledge.Graph, error)
	// domain loads the graph without its symbol shards, the graph whose blast radius
	// `magus explain` prints.
	domain func(context.Context) (*knowledge.Graph, error)

	staleOnce   sync.Once
	staleIdx    []types.KnowledgeStaleIndex
	scopeOnce   sync.Once
	scope       []string
	graph       lazyGraph
	domainGraph lazyGraph
}

// lazyGraph is one graph loaded on the first get and handed to every later one, or the
// same nil when skip declined the load or it failed, so a failure is not retried per ask.
type lazyGraph struct {
	once sync.Once
	g    *knowledge.Graph
}

func (l *lazyGraph) get(ctx context.Context, skip func(context.Context) bool, build func(context.Context) (*knowledge.Graph, error)) *knowledge.Graph {
	l.once.Do(func() {
		if skip(ctx) {
			return
		}
		if loaded, err := build(ctx); err == nil {
			l.g = loaded
		}
	})
	return l.g
}

// New is the graph of the workspace at root.
func New(root string, rebuild bool, env Env) *Graph {
	return &Graph{
		rebuild: rebuild,
		workspace: func(ctx context.Context) (*magus.Magus, error) {
			return env.LoadMagus(ctx, root)
		},
		stale: func(ctx context.Context) []types.KnowledgeStaleIndex {
			return rootStaleIndexes(ctx, root, env)
		},
		build: func(ctx context.Context) (*knowledge.Graph, error) {
			return env.RootRefsGraph(ctx, root)
		},
		domain: func(ctx context.Context) (*knowledge.Graph, error) {
			return env.RootDomainGraph(ctx, root)
		},
	}
}

// rootStaleIndexes judges afresh from the opened workspace, which names the
// changed files a recorded verdict does not carry.
func rootStaleIndexes(ctx context.Context, root string, env Env) []types.KnowledgeStaleIndex {
	m, err := env.LoadMagus(ctx, root)
	if err != nil {
		return env.RootStale(ctx, root)
	}
	return env.FindStale(ctx, m)
}

// newTree is New for a checkout the process did not open at startup. It
// opens tree's own workspace, once, beside the process-wide singleton rather than through
// it, which names a single workspace and panics on a second. The settings are the process's:
// a checkout reads the config the invoking tree loaded.
func newTree(tree string, rebuild bool, env Env) *Graph {
	var (
		once sync.Once
		m    *magus.Magus
		err  error
	)
	open := func(ctx context.Context) (*magus.Magus, error) {
		once.Do(func() {
			var root string
			if root, err = workspace.FindRoot(tree); err != nil {
				return
			}
			opts := append(slices.Clone(env.Options), workspace.WithLoadedConfig(env.Config), magus.WithVersion(env.Version))
			m, err = magus.Open(ctx, root, opts...)
		})
		return m, err
	}
	return &Graph{
		rebuild:   rebuild,
		workspace: open,
		stale: func(ctx context.Context) []types.KnowledgeStaleIndex {
			m, err := open(ctx)
			if err != nil {
				return nil
			}
			return env.FindStale(ctx, m)
		},
		build: func(ctx context.Context) (*knowledge.Graph, error) {
			m, err := open(ctx)
			if err != nil {
				return nil, err
			}
			return env.RefsGraph(ctx, m)
		},
		domain: func(ctx context.Context) (*knowledge.Graph, error) {
			m, err := open(ctx)
			if err != nil {
				return nil, err
			}
			return env.DomainGraph(ctx, m)
		},
	}
}

// Load is the symbol graph, or nil when it would not load or must not be read.
func (r *Graph) Load(ctx context.Context) *knowledge.Graph {
	return r.graph.get(ctx, r.mustNotRead, announcedLoad("the symbol graph", r.build))
}

// announcedLoad says what a load waits on, then what it took. The load
// rebuilds a stale index for minutes, and lazyGraph hides its failure.
func announcedLoad(what string, load func(context.Context) (*knowledge.Graph, error)) func(context.Context) (*knowledge.Graph, error) {
	return func(ctx context.Context) (*knowledge.Graph, error) {
		slog.InfoContext(ctx, fmt.Sprintf("loading %s; a missing or stale symbol index is rebuilt first, which can take minutes", what), attr.Notice("note"))
		began := time.Now()
		g, err := load(ctx)
		took := cache.FormatDuration(time.Since(began))
		if err != nil {
			slog.InfoContext(ctx, fmt.Sprintf("could not load %s after %s", what, took), attr.Error(err), attr.Notice("note"))
			return g, err
		}
		slog.InfoContext(ctx, fmt.Sprintf("loaded %s in %s", what, took), attr.Notice("note"))
		return g, nil
	}
}

// LoadScoped is Load for a row writing paths.
func (r *Graph) LoadScoped(paths []string) func(context.Context) *knowledge.Graph {
	return func(ctx context.Context) *knowledge.Graph {
		r.setScope(paths)
		return r.Load(ctx)
	}
}

// LoadDomain is LoadScoped for the domain graph, under the same rule.
func (r *Graph) LoadDomain(ctx context.Context, paths []string) *knowledge.Graph {
	r.setScope(paths)
	return r.domainGraph.get(ctx, r.mustNotRead, announcedLoad("the domain graph", r.domain))
}

func (r *Graph) setScope(paths []string) {
	r.scopeOnce.Do(func() { r.scope = paths })
}

// mustNotRead reports whether a read that must not build faces a stale symbol index that
// could hold a change inside the write paths.
func (r *Graph) mustNotRead(ctx context.Context) bool {
	return !r.rebuild && len(r.StaleIndexes(ctx)) > 0
}

// StaleIndexes is the stale symbol indexes that keep a read from the graph, judged once
// against the write paths the first scoped read named; with none named, every stale index.
func (r *Graph) StaleIndexes(ctx context.Context) []types.KnowledgeStaleIndex {
	r.staleOnce.Do(func() {
		r.setScope(nil)
		for _, s := range r.stale(ctx) {
			if staleIndexHits(s, r.scope) {
				r.staleIdx = append(r.staleIdx, s)
			}
		}
	})
	return r.staleIdx
}

// staleIndexHits reports whether s could hold a change inside paths: it names no changed
// file or only some, paths is empty, or a file it names falls inside one of them.
func staleIndexHits(s types.KnowledgeStaleIndex, paths []string) bool {
	if len(paths) == 0 || s.ChangedCount == 0 || len(s.Changed) < s.ChangedCount {
		return true
	}
	return slices.ContainsFunc(s.Changed, func(file string) bool {
		return slices.ContainsFunc(paths, func(p string) bool { return jobtypes.PathsIntersect(p, file) })
	})
}

// ExtendedFootprint adds to rec's write paths the tests beside what it writes.
// It permits the outputs they regenerate as the integrator's. Each kind gets a
// count line. It skips a bound caller and a workspace that did not load.
func ExtendedFootprint(ctx context.Context, store *job.Store, graph *Graph, root string, rec jobtypes.Declaration, ws job.Workspace) (jobtypes.Declaration, []string) {
	if store.Actor().Bound() || ws.Unavailable != "" {
		return rec, nil
	}
	var declared jobtypes.Job
	rec.Apply(&declared)
	// Merging every symbol shard costs a GB of heap, so it waits for a row whose
	// footprint referrers could extend.
	var referrers func(file, declaration string) map[string]int
	if job.FootprintMayExtend(root, declared) {
		if g := graph.LoadScoped(declared.Paths.Write)(ctx); g != nil && g.HasSymbols() {
			referrers = g.Referrers
		}
	}
	paths, _ := job.ExtendFootprint(root, declared, ws.Projects, referrers)
	rec.Paths.Write = append(rec.Paths.Write, paths...)
	rec.Paths.Soft = append(rec.Paths.Soft, paths...)
	declared.Paths.Write = append(declared.Paths.Write, paths...)
	deviations := job.IntegratorDeviations(root, declared, ws.Projects, ws.Ignored)
	rec.Deviations = append(rec.Deviations, deviations...)
	var notes []string
	if len(paths) > 0 {
		notes = append(notes, fmt.Sprintf("%d test file(s) beside the write paths were added to them: -o json lists each", len(paths)))
	}
	if len(deviations) > 0 {
		notes = append(notes, fmt.Sprintf("%d regenerated output(s) outside the write paths are permitted: the integrator owns them", len(deviations)))
	}
	return rec, notes
}

// ByTree answers a graph per checkout, root's being rootGraph and any other built on
// first ask under the same rebuild rule.
func ByTree(root string, rootGraph *Graph, rebuild bool, env Env) func(tree string) *Graph {
	g := &treeGraphs{root: filepath.Clean(root), rootGraph: rootGraph, rebuild: rebuild, env: env, others: map[string]*Graph{}}
	return g.in
}

type treeGraphs struct {
	root      string
	rootGraph *Graph
	rebuild   bool
	env       Env
	mu        sync.Mutex
	others    map[string]*Graph
}

func (t *treeGraphs) in(tree string) *Graph {
	tree = filepath.Clean(tree)
	if tree == t.root {
		return t.rootGraph
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.others[tree]; !ok {
		t.others[tree] = newTree(tree, t.rebuild, t.env)
	}
	return t.others[tree]
}
