package client

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"sync"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/libs/mergequeue/types"

	// Without the engine no magusfile is evaluated, and every path would be attributed
	// to its project by directory alone, a wrong affected set with no error.
	_ "github.com/egladman/magus/internal/interp"
	_ "github.com/egladman/magus/internal/interp/bindings"
	_ "github.com/egladman/magus/internal/interp/engine/buzz"
)

// Workspace is [types.BuildFacts] from a magus workspace, loaded once, in place of
// running `magus affected --plan --stdin` and `magus describe file` per change. Open it
// on the base's checkout: its answers are the base's declarations.
type Workspace struct {
	m      *magus.Magus
	target string
	// mu serializes Plan, which the SDK does not document as safe for concurrent use;
	// in process it is fast enough that the queue's parallel admission loses little.
	mu sync.Mutex
}

var _ types.BuildFacts = (*Workspace)(nil)

// OpenWorkspace opens the magus workspace at root. target is the CI target the affected
// set is computed for, typically "ci". The caller owns Close.
func OpenWorkspace(ctx context.Context, root, target string) (*Workspace, error) {
	if target == "" {
		return nil, errors.New("workspace needs the target its affected sets are computed for")
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
func (w *Workspace) Affected(ctx context.Context, _ types.Change, paths []string) ([]string, string, error) {
	if len(paths) == 0 {
		return []string{}, "", nil
	}
	return w.plan(ctx, paths)
}

func (w *Workspace) plan(ctx context.Context, paths []string) ([]string, string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// The queue reads the closure, never the shards cut from it, so no shard cap applies.
	plan, err := w.m.Plan(ctx, w.target, magus.PlanOptions{ChangedPaths: paths, MaxShards: -1})
	if err != nil {
		return nil, "", err
	}
	return plan.Affected, plan.UnboundedBy, nil
}

// Outputs reports which of paths some project declares as an output: `magus describe
// file`'s output role. A path only a VCS attribute marks generated is not one.
func (w *Workspace) Outputs(ctx context.Context, paths []string) (map[string]bool, error) {
	entries, err := w.m.ClassifyFiles(ctx, paths)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(paths))
	for i, e := range entries {
		if len(e.OutputOf) > 0 {
			out[paths[i]] = true
		}
	}
	return out, nil
}

// Generation names the projects declaring outputs as the units that regenerate them,
// and proves from the project graph which of changed their regeneration could run. A
// changed path is code unless it is documentation, text or an image, and a code path
// runs in the regeneration when a generating project declares it as a source or input
// (`magus describe file`), or when it reaches one through the affected closure (`magus
// affected --plan --stdin`). A path the closure cannot bound, such as a magusfile, a
// spell, the workspace config, a lockfile or a toolchain pin, is never a proof. The proof
// is as sound as the workspace's declarations, the same premise its affected sets rest
// on.
func (w *Workspace) Generation(ctx context.Context, outputs, changed []string) (types.Generation, error) {
	entries, err := w.m.ClassifyFiles(ctx, outputs)
	if err != nil {
		return types.Generation{}, err
	}
	var g types.Generation
	for _, e := range entries {
		if len(e.OutputOf) == 0 {
			g.Unbounded = "no project declares " + e.Path + " as its output"
			return g, nil
		}
		g.Units = append(g.Units, e.OutputOf...)
	}
	g.Units = slices.Compact(slices.Sorted(slices.Values(g.Units)))
	code := slices.DeleteFunc(slices.Clone(changed), isData)
	if len(code) == 0 {
		return g, nil
	}
	declared, err := w.m.ClassifyFiles(ctx, code)
	if err != nil {
		return types.Generation{}, err
	}
	for i, p := range code {
		if slices.ContainsFunc(declared[i].SourceOf, func(u string) bool { return slices.Contains(g.Units, u) }) {
			g.Code = append(g.Code, p)
			continue
		}
		reached, unbounded, err := w.plan(ctx, []string{p})
		if err != nil {
			return types.Generation{}, err
		}
		switch {
		case unbounded != "":
			g.Unbounded = unbounded
			g.Code = append(g.Code, p)
		case slices.ContainsFunc(reached, func(u string) bool { return slices.Contains(g.Units, u) }):
			g.Code = append(g.Code, p)
		}
	}
	return g, nil
}

// dataExtensions are what a regeneration reads and never runs. Anything else, a format
// this list does not know included, counts as code.
var dataExtensions = []string{".md", ".markdown", ".txt", ".rst", ".adoc", ".csv", ".tsv",
	".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".pdf"}

func isData(p string) bool {
	return slices.Contains(dataExtensions, strings.ToLower(path.Ext(p)))
}

// Close releases the workspace.
func (w *Workspace) Close() error { return w.m.Close() }
