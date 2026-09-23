package client

import (
	"context"
	"errors"
	"sync"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/libs/mergequeue"

	// Without the engine no magusfile is evaluated, and every path would be attributed
	// to its project by directory alone, a wrong affected set with no error.
	_ "github.com/egladman/magus/internal/interp"
	_ "github.com/egladman/magus/internal/interp/bindings"
	_ "github.com/egladman/magus/internal/interp/engine/buzz"
)

// Workspace is [mergequeue.BuildFacts] from a magus workspace, loaded once, in place of
// running `magus affected --plan --stdin` per change.
type Workspace struct {
	m      *magus.Magus
	target string
	// mu serializes Plan, which the SDK does not document as safe for concurrent use;
	// in process it is fast enough that the queue's parallel admission loses little.
	mu sync.Mutex
}

var _ mergequeue.BuildFacts = (*Workspace)(nil)

// OpenWorkspace opens the magus workspace at root. target is the CI target the affected
// set is computed for, typically "ci". The caller owns Close.
func OpenWorkspace(ctx context.Context, root, target string) (*Workspace, error) {
	if target == "" {
		return nil, errors.New("a Workspace needs the target its affected sets are computed for")
	}
	m, err := magus.Open(ctx, root)
	if err != nil {
		return nil, err
	}
	return &Workspace{m: m, target: target}, nil
}

// Affected returns the projects paths reach through the project graph, and why that set
// is not a proof when paths edit the declarations it was computed from or files no
// project claims.
func (w *Workspace) Affected(ctx context.Context, _ mergequeue.Change, paths []string) ([]string, string, error) {
	if len(paths) == 0 {
		return []string{}, "", nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	// The queue reads the closure, never the shards cut from it, so no shard cap applies.
	plan, err := w.m.Plan(ctx, w.target, magus.PlanOptions{ChangedPaths: paths, MaxShards: -1})
	if err != nil {
		return nil, "", err
	}
	return plan.Affected, plan.UnboundedBy, nil
}

// Close releases the workspace.
func (w *Workspace) Close() error { return w.m.Close() }
