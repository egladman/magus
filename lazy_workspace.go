package magus

import (
	"context"
	"io"
	"sync"
	"sync/atomic"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// LazyWorkspace is a workspace a knowledge read opens only when it must. Opening one
// evaluates every magusfile in the tree, which a read that the stored graph answers never
// needs: measured 2026-10-02 on this repository, 425ms of a warm `magus query`'s 590ms.
// So a read holds one of these instead, and the Inspector calls a rebuild makes
// (TargetGraph, ListProjects) are what open it, on the first call and once. Root is known
// without opening, which is all the stamps and the store need.
//
// It is a *Magus that has not happened yet, not a different kind of workspace: every
// answer comes from the handle open returns, and the read-path helpers that recognize a
// *Magus (remoteShards, UsePublishedShards) recognize this and defer the same way.
type LazyWorkspace struct {
	root string
	open func(context.Context) (*Magus, error)

	once   sync.Once
	m      *Magus
	err    error
	opened atomic.Bool

	// walkOnce memoizes tree: the stamps a read computes and the coverage probe that
	// follows it fold the same walk, which is most of what a read the store answers costs.
	walkOnce sync.Once
	walk     *knowledge.TreeWalk

	// published is what UsePublishedShards installed before the workspace opened; it is
	// handed to the *Magus the moment it exists, so a read that opens late still restores
	// evicted shards from the published graph.
	published atomic.Pointer[knowledge.RemoteShards]
}

// NewLazyWorkspace returns a workspace rooted at root that open produces on first use.
// root is the resolved workspace root (see FindRoot), never an override to resolve.
func NewLazyWorkspace(root string, open func(context.Context) (*Magus, error)) *LazyWorkspace {
	return &LazyWorkspace{root: root, open: open}
}

// Root returns the workspace root without opening the workspace.
func (l *LazyWorkspace) Root() string { return l.root }

// Magus opens the workspace, once, and returns it; every later call returns the same
// handle or the same error. A failure is not retried: the caller that needed the model
// reports it, as an eager open would have at startup.
func (l *LazyWorkspace) Magus(ctx context.Context) (*Magus, error) {
	l.once.Do(func() {
		l.m, l.err = l.open(ctx)
		// opened is published BEFORE the hand-off below reads published, and
		// usePublishedShards stores published BEFORE it reads opened: with both orders,
		// one side or the other sees the other's write, so a backing installed while the
		// open was in flight reaches the handle either way.
		l.opened.Store(true)
		if l.err == nil && l.m != nil {
			if p := l.published.Load(); p != nil {
				l.m.publishedShards.Store(p)
			}
		}
	})
	return l.m, l.err
}

// Opened reports whether a call has opened the workspace yet, without opening it.
func (l *LazyWorkspace) Opened() bool { return l.opened.Load() }

// tree is the one walk of the workspace tree this read makes, taken on first use. A
// LazyWorkspace is one read's view of the tree, so a walk taken at its start holds for
// everything that read computes; the server, which answers many reads from one handle,
// never holds one of these and walks per read as before.
func (l *LazyWorkspace) tree() *knowledge.TreeWalk {
	l.walkOnce.Do(func() { l.walk = knowledge.WalkTree(l.root) })
	return l.walk
}

// usePublishedShards records r for the workspace, open or not.
func (l *LazyWorkspace) usePublishedShards(r knowledge.RemoteShards) {
	l.published.Store(&r)
	if l.opened.Load() && l.m != nil {
		l.m.publishedShards.Store(&r)
	}
}

// The types.Inspector methods, each call opening the workspace on first use. A failed open
// is every call's error.

func (l *LazyWorkspace) ListCharms(ctx context.Context) ([]types.CharmEntry, error) {
	m, err := l.Magus(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListCharms(ctx)
}

func (l *LazyWorkspace) ListTargets(ctx context.Context) ([]types.TargetEntry, error) {
	m, err := l.Magus(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListTargets(ctx)
}

func (l *LazyWorkspace) ListProjects(ctx context.Context) (types.ProjectsOutput, error) {
	m, err := l.Magus(ctx)
	if err != nil {
		return types.ProjectsOutput{}, err
	}
	return m.ListProjects(ctx)
}

func (l *LazyWorkspace) EvaluateProjects(ctx context.Context) (types.EvaluatedProjectsOutput, error) {
	m, err := l.Magus(ctx)
	if err != nil {
		return types.EvaluatedProjectsOutput{}, err
	}
	return m.EvaluateProjects(ctx)
}

func (l *LazyWorkspace) EvaluateTarget(ctx context.Context, t types.Target) ([]types.EvaluatedTarget, error) {
	m, err := l.Magus(ctx)
	if err != nil {
		return nil, err
	}
	return m.EvaluateTarget(ctx, t)
}

func (l *LazyWorkspace) ClassifyFiles(ctx context.Context, paths []string) ([]types.FileEntry, error) {
	m, err := l.Magus(ctx)
	if err != nil {
		return nil, err
	}
	return m.ClassifyFiles(ctx, paths)
}

func (l *LazyWorkspace) TargetGraph(ctx context.Context) (types.TargetGraphOutput, error) {
	m, err := l.Magus(ctx)
	if err != nil {
		return types.TargetGraphOutput{}, err
	}
	return m.TargetGraph(ctx)
}

func (l *LazyWorkspace) Workspace(ctx context.Context, cfg types.WorkspaceConfig) (types.WorkspaceEntry, error) {
	m, err := l.Magus(ctx)
	if err != nil {
		return types.WorkspaceEntry{}, err
	}
	return m.Workspace(ctx, cfg)
}

// lazyRemoteShards is remoteShards over a LazyWorkspace: the backing is resolved from the
// opened *Magus on the first shard read or write, which only a store that must restore an
// evicted shard or push a rebuilt one ever makes. A read the stored shards answer never
// opens the workspace for its remote.
type lazyRemoteShards struct{ ws *LazyWorkspace }

func (r lazyRemoteShards) backing(ctx context.Context) (knowledge.RemoteShards, error) {
	m, err := r.ws.Magus(ctx)
	if err != nil {
		return nil, err
	}
	return remoteShards(m), nil
}

func (r lazyRemoteShards) GetShard(ctx context.Context, key string) (io.ReadCloser, error) {
	b, err := r.backing(ctx)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, knowledge.ErrShardMiss
	}
	return b.GetShard(ctx, key)
}

func (r lazyRemoteShards) PutShard(ctx context.Context, key string, body io.Reader) error {
	b, err := r.backing(ctx)
	if err != nil {
		return err
	}
	if b == nil {
		return nil // local-only, as a nil remote is: nothing to push to
	}
	return b.PutShard(ctx, key, body)
}
