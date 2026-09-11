package magus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/internal/cache"
	interp "github.com/egladman/magus/internal/interp"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/types"
)

// deriveBatchOrder collects the batch's target-granular footprints (each step's
// own target plus its static chain closure) and derives writer-before-reader
// ordering from them. The DerivedOrder's RunAfter entries are applied onto the
// steps in place; unordered edges are left for settleDerivedOrder after the
// batch.
//
// It returns an error for the overlaps inside one step that nothing sequences
// (MGS4008). Those are not a schedule to improve: no step order can put one
// chain member ahead of another in the same chain, and the run is refused here,
// before any goroutine launches, rather than after the reader has either read
// stale bytes or wedged the pool waiting for the writer.
//
// A one-step batch is derived too, where it used to return early. Cross-step
// order is indeed vacuous there, but the same-step question is not: the shape
// this refuses fits entirely inside one `ci` step's chain, which is the batch a
// single-project gate runs.
func (m *Magus) deriveBatchOrder(ctx context.Context, steps []cache.Step) (*cache.DerivedOrder, error) {
	if len(steps) == 0 {
		return &cache.DerivedOrder{}, nil
	}
	order := cache.DeriveTargetOrder(steps, m.collectOrderNodes(steps), cache.WorkspaceOverlapWitness(m.Root()))
	if err := order.SameStep.Refusal(); err != nil {
		return nil, err
	}
	if advice := order.SameStep.Advice(); advice != "" {
		// Cross-project pairs are reported, not refused: their sequencing may belong
		// to another project's magusfile, and doctor lists every one.
		slog.WarnContext(ctx, advice)
	}
	for i := range steps {
		key := cache.DepKey(steps[i].ProjectPath, steps[i].Target)
		for _, up := range order.RunAfter[key] {
			if !slices.Contains(steps[i].RunAfter, up) {
				steps[i].RunAfter = append(steps[i].RunAfter, up)
			}
		}
	}
	// Answers "why did these steps serialize" / "why did that target re-run" at -vv
	// without a debugger, like the barrier's schedule.wait trace.
	trace := func(e cache.DerivedEdge, dropped string) {
		slog.DebugContext(ctx, "schedule.derived",
			slog.String("writer", order.Nodes[e.Writer].Project+":"+order.Nodes[e.Writer].Target),
			slog.String("reader", order.Nodes[e.Reader].Project+":"+order.Nodes[e.Reader].Target),
			slog.Bool("ordered", e.Ordered), slog.String("dropped", dropped))
	}
	for _, e := range order.Edges {
		trace(e, "")
	}
	for _, e := range order.Dropped {
		trace(e.DerivedEdge, e.Reason)
	}
	return order, nil
}

// collectOrderNodes builds one TargetNode per distinct (project, target) the
// batch executes: the steps' own targets and, transitively, every chain member
// their bodies compose (ctx.needs, extracted statically). Globs are
// workspace-rooted the same way the cache keys them (joinGlob).
func (m *Magus) collectOrderNodes(steps []cache.Step) []cache.TargetNode {
	byKey := map[string]*cache.TargetNode{}
	var order []string

	add := func(p *types.Project, target, stepKey string) *cache.TargetNode {
		key := cache.DepKey(p.Path, target)
		n, ok := byKey[key]
		if !ok {
			// The target's OWN declarations, not buildStep's folded view: the step
			// folds composed targets' updates and skip-cache outputs into the parent
			// (they land inside its window), but for ordering each chain member is
			// its own node and the fold would blame a member's writes on its
			// composer. Measured: format's modifiesExistingFiles("**/*.go") reached
			// test's writes that way and closed a declared-footprint "cycle" between
			// test and docs content-generate that neither target declares.
			node := cache.DeclaredNode(p, target, stepKey, m.ws.Get)
			s := m.buildStep(p, target)
			if !node.DeclaredReads {
				node.Reads = s.Sources
			}
			if len(p.TargetOutputs[target]) == 0 {
				// No ctx.writesFiles: the project/spell output baseline (dist/**, ...)
				// is the only claim there is. Weak, like a fallback read.
				node.Writes = append(node.Writes, s.Outputs...)
			}
			node.IgnoreDirs = s.IgnoreDirs
			n = &node
			byKey[key] = n
			order = append(order, key)
		}
		if !slices.Contains(n.Steps, stepKey) {
			n.Steps = append(n.Steps, stepKey)
		}
		return n
	}

	for _, s := range steps {
		stepKey := cache.DepKey(s.ProjectPath, s.Target)
		_ = types.WalkChain(m.ws.Get(s.ProjectPath), s.Target, m.ws.Get, func(v types.ChainVisit) error {
			add(v.Project, v.Target, stepKey)
			return nil
		})
	}

	nodes := make([]cache.TargetNode, 0, len(order))
	for _, k := range order {
		nodes = append(nodes, *byKey[k])
	}
	return nodes
}

// orderSettle carries what settling needs across the batch: the derived edges
// and each cross-edge writer's output bytes as they stood before this
// invocation wrote anything.
type orderSettle struct {
	order  *cache.DerivedOrder
	before map[int]string
}

// prepareOrderSettle hashes every cross-edge writer's declared writes before the
// batch runs, so settling can tell a writer that produced new bytes from one
// that rewrote what was already there. Dropped edges count: their readers are
// exactly the ones no schedule could serve. Nil when no edge can need settling.
func (m *Magus) prepareOrderSettle(order *cache.DerivedOrder) *orderSettle {
	if order == nil || len(order.Edges)+len(order.Dropped) == 0 {
		return nil
	}
	s := &orderSettle{order: order, before: map[int]string{}}
	hash := func(w int) {
		if _, ok := s.before[w]; !ok {
			s.before[w] = m.hashNodeWrites(order.Nodes[w])
		}
	}
	for _, e := range order.Edges {
		hash(e.Writer)
	}
	for _, e := range order.Dropped {
		hash(e.Writer)
	}
	return s
}

// settleDerivedOrder re-runs, once and in dependency order, each skip-cache
// chain-member target that declares writes of its own and whose reads a
// later-running target's writes overlapped. This is the entangled shape where
// two steps' chains write into each other's read sets, so no step order can put
// every writer first (at project granularity it is a cycle; the chain is only
// acyclic target by target). It is also where a genuine cycle lands, each sibling
// routing index reading what the others write. A reader re-runs only when a violated writer
// actually changed bytes, so a settled tree settles to a no-op and a second
// identical invocation re-runs nothing.
//
// Step targets themselves never re-run here: a composing target like generate
// is its own gate (it hashes drift across its body's window) and re-running it
// would re-run its whole chain. Members of a step that replayed from cache are
// skipped too, since their outputs came from the entry, not from execution.
func (m *Magus) settleDerivedOrder(ctx context.Context, st *orderSettle, steps []cache.Step, results []cache.Result,
	newStep func(*types.Project, string) cache.Step, opts []cache.RunOption,
) error {
	if st == nil {
		return nil
	}
	// No bound of its own. This pass carried a hardcoded ten-minute ceiling while it
	// was invisible: dispatched outside the journal and the inflight set, a wedged
	// re-run here read as a finished run that forgot to exit. RunAside below made it
	// visible and the stall watchdog now fails it by name, so what a ceiling here would
	// still add is killing a settle that is making steady progress, on a figure nobody
	// declared. A target that needs one declares `timeout`, which reaches these re-runs
	// through the same seam as any other body (internal/interp.withDeclaredCeiling).
	order := st.order

	replayed := map[string]bool{}
	stepKeys := map[string]bool{}
	for i, s := range steps {
		k := cache.DepKey(s.ProjectPath, s.Target)
		stepKeys[k] = true
		if i < len(results) && results[i].Hit {
			replayed[k] = true
		}
	}

	// moved: the writer's bytes changed during the batch, so a reader the schedule
	// could not put after it read stale content. reranMoved: the writer re-ran
	// during settling and changed bytes again, which stales even readers the batch
	// order had satisfied.
	moved := map[int]bool{}
	reranMoved := map[int]bool{}
	current := map[int]string{}
	for w, before := range st.before {
		current[w] = m.hashNodeWrites(order.Nodes[w])
		moved[w] = current[w] != before
	}

	inEdges := map[int][]cache.DerivedEdge{}
	for _, e := range order.Edges {
		inEdges[e.Reader] = append(inEdges[e.Reader], e)
	}
	// A dropped edge is permanently unordered: cycle resolution removed it from the
	// schedule precisely because no step order can honor it.
	for _, e := range order.Dropped {
		inEdges[e.Reader] = append(inEdges[e.Reader], e.DerivedEdge)
	}

	for _, r := range order.TopoNodes() {
		node := order.Nodes[r]
		if stepKeys[node.Key()] {
			continue
		}
		if slices.ContainsFunc(node.Steps, func(s string) bool { return replayed[s] }) {
			continue
		}
		// Verifiers (no declared writes) never re-run here. Settling exists to fix
		// stale bytes a later writer invalidated, and a target that declares no
		// writes has no bytes to fix: its product is a verdict, re-verified by the
		// next invocation anyway (it is skip-cache, see below). Without this guard a
		// docs prose move re-ran root's security scan (needs(generate) plus
		// govulncheck plus trivy) on the silent post-batch tail, and a
		// network-stalled scanner there wedged the 2026-09-04 gate for over an hour
		// with every project lock held and nothing visibly running.
		if !node.DeclaredWrites {
			continue
		}
		var stale *cache.TargetNode
		for _, e := range inEdges[r] {
			if (!e.Ordered && moved[e.Writer]) || reranMoved[e.Writer] {
				stale = &order.Nodes[e.Writer]
				break
			}
		}
		if stale == nil {
			continue
		}
		p := m.ws.Get(node.Project)
		if p == nil {
			continue
		}
		// Skip-cache readers only. A cacheable reader's key covers the moved input
		// (or under-declares it, which is its own bug), so the stale result cannot
		// replay: the next invocation misses and re-runs it inside a proper cache
		// window. Re-running it here would execute outside any window, record no
		// entry, and put suite-scale targets on this tail (root's test declares it
		// reads **/*.md, so any docs prose movement would re-run the whole suite).
		// A skip-cache reader is the one with no keyed convergence to lean on, and
		// it is the motivating shape: graph-generate re-runs every invocation, in
		// the same wrong order every time.
		if !p.TargetPolicies[node.Target].SkipCache {
			continue
		}
		slog.InfoContext(ctx, "magus: derived ordering: re-running target whose declared inputs were written after it ran",
			slog.String("project", node.Project), slog.String("target", node.Target),
			slog.String("written_by", stale.Project+":"+stale.Target))
		types.ActiveDispatchFromContext(ctx).Mark(p.Path)
		types.ActiveDispatchFromContext(ctx).Mark(p.Dir)
		// Through RunAside, not straight at the interpreter: a re-run here holds every
		// project lock, so it has to appear where the batch's steps appear: a limiter
		// slot, a machine claim `magus status` names, the inflight set, and a journal
		// result event with its captured log. Dispatched bare it was invisible to all
		// four, and a stalled settle read as a finished run that forgot to exit.
		if _, err := m.cache.RunAside(ctx, newStep(p, node.Target), func(ctx context.Context) error {
			_, err := interp.RunDir(buzz.WithTargetMemo(ctx, buzz.NewTargetMemo()), p.Dir, node.Target, nil)
			return err
		}, opts...); err != nil {
			return fmt.Errorf("magus: derived ordering: settle %s:%s: %w", node.Project, node.Target, err)
		}
		if before, ok := current[r]; ok {
			current[r] = m.hashNodeWrites(node)
			reranMoved[r] = current[r] != before
		}
	}
	return nil
}

// hashNodeWrites digests the bytes currently on disk under the node's write
// globs. Only equality is ever compared, so any stable digest works.
func (m *Magus) hashNodeWrites(n cache.TargetNode) string {
	root := m.Root()
	fsys := os.DirFS(root)
	var files []string
	for _, glob := range n.Writes {
		matches, err := doublestar.Glob(fsys, glob)
		if err != nil {
			continue
		}
		files = append(files, matches...)
	}
	slices.Sort(files)
	files = slices.Compact(files)

	h := sha256.New()
	for _, rel := range files {
		f, err := os.Open(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		if fi, err := f.Stat(); err != nil || fi.IsDir() {
			_ = f.Close()
			continue
		}
		fmt.Fprintf(h, "%s\x00", rel)
		_, _ = io.Copy(h, f)
		_ = f.Close()
	}
	return hex.EncodeToString(h.Sum(nil))
}
