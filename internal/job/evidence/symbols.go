package evidence

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// LeaseGraphEvidence resolves each write path against the knowledge graph, one line per
// path the graph knows. cold reports a graph that would not load at all.
//
// A path the graph cannot resolve is skipped SILENTLY, because most write paths are
// ordinary source directories the containment tree does not carry, and a "no node" line
// per path would bury the ones that do resolve. A cold graph is different and is
// reported once: "not asked" must not read as "nothing depends on this". It takes the domain
// graph through graph, so a graph that must not build answers cold while a stale symbol
// index may hold changes inside paths.
func LeaseGraphEvidence(ctx context.Context, graph *Graph, paths []string) (evidence []job.TermsEvidence, cold bool) {
	if len(paths) == 0 {
		return nil, false
	}
	g := graph.LoadDomain(ctx, paths)
	if g == nil {
		return nil, true
	}
	for _, p := range paths {
		if e, ok := pathEvidence(g, p); ok {
			evidence = append(evidence, e)
		}
	}
	return evidence, false
}

// pathEvidence answers for one declared path, trying the node ids a path can carry in the
// containment tree.
//
// It accepts only a node whose id IS the ref it asked for. Explain resolves a bare name
// fuzzily, which is right for a person typing `magus explain build` and wrong here: asked
// for "cmd/magus" it answered target:.:release-sign, and a brief that hands a worker the
// blast radius of an unrelated node is worse than one that stays quiet.
func pathEvidence(g *knowledge.Graph, declared string) (job.TermsEvidence, bool) {
	for _, ref := range []string{fmt.Sprintf("%s:%s", types.KindDir, declared), fmt.Sprintf("%s:%s", types.KindFile, declared), declared} {
		out, ok := g.Explain(ref)
		if !ok || out.Node.ID != ref {
			continue
		}
		return job.TermsEvidence{Path: declared, Node: out.Node.ID, BlastRadius: out.BlastRadius}, true
	}
	return job.TermsEvidence{}, false
}

// JobSymbolReader loads the knowledge graph ONCE and answers every symbol a job's gates
// name from it.
//
// Lazy and memoized because a verification reads several names and the graph is the
// expensive part; loading per name would reopen it once per symbol. A load failure is
// remembered too, so a verification does not retry a graph that is not there once per
// gate and report the same failure five times. Without rebuild, a stale symbol index
// answers no name at all: this reader has no write paths to clear it against (see Graph).
func JobSymbolReader(root string, rebuild bool, env Env) job.SymbolReader {
	return GraphSymbolReader(New(root, rebuild, env).Load)
}

// GraphSymbolReader answers each symbol a gate names from the graph load returns.
func GraphSymbolReader(load func(context.Context) *knowledge.Graph) job.SymbolReader {
	return func(ctx context.Context, name, signature string) (job.SymbolFact, bool) {
		g := load(ctx)
		if g == nil {
			return job.SymbolFact{}, false
		}
		return job.GraphSymbols(g)(ctx, name, signature)
	}
}
