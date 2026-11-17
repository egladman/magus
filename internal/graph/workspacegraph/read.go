package workspacegraph

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/embedtable"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/libs/oci"
	"github.com/egladman/magus/types"
)

// ReadWorkspace is what a graph read needs of its workspace: the root, and the Inspector
// facts a rebuild reads. The *magus.Magus a server holds satisfies it, and so does the
// *magus.LazyWorkspace a CLI read opens, which evaluates the workspace only if the stored
// graph turns out stale.
type ReadWorkspace interface {
	types.Inspector
	Root() string
}

// CoverageFunc says what the symbol layer covers for a read of input, as
// Reads.Measure does.
type CoverageFunc func(input string, seeded bool) knowledge.Coverage

// Reads runs the graph reads on the two things only the root magus package can supply, and
// this package cannot import it: Measure observes the symbol layer a read could not search,
// and Full opens a lazy workspace into the whole set of workspace operations.
type Reads struct {
	// Measure is the symbol coverage of ws under cfg for a read of input.
	Measure func(ctx context.Context, ws ReadWorkspace, cfg config.Config, input string, seeded bool) knowledge.Coverage
	// Full is the whole set of workspace operations behind ws, opening a lazy one: for the
	// reads that need more than the Inspector (the global graph unions WorkspaceRepository
	// handles).
	Full func(ctx context.Context, ws ReadWorkspace) (types.WorkspaceRepository, error)
}

// MeasuredCoverage measures ws under cfg afresh on every call, as a single read does.
func (r Reads) MeasuredCoverage(ctx context.Context, ws ReadWorkspace, cfg config.Config) CoverageFunc {
	return func(input string, seeded bool) knowledge.Coverage {
		return r.Measure(ctx, ws, cfg, input, seeded)
	}
}

// SearchGraph answers read from the graph ws builds under cfg.
func (r Reads) SearchGraph(ctx context.Context, ws ReadWorkspace, cfg config.Config, read knowledge.GraphRead, refresh, global bool) (knowledge.QueryResult, error) {
	tr := observability.TraceFromContext(ctx)
	seeded := knowledge.SeedsLazyLayer(read.Input)
	// A reader context the VCS cannot give ranks without it, and the ranking record says so:
	// a shallow clone with no merge base still answers the query.
	stop := tr.Phase("query.reader_context")
	reader, readerErr := ReaderContext(ctx, ws, ws.Root())
	stop()
	ctx = knowledge.WithTable(knowledge.WithReader(ctx, reader), embedtable.Cached)
	var g *knowledge.Graph
	var out types.KnowledgeQueryOutput
	var err error
	if seeded && !global {
		// The answer LoadGraph then Query give, ranked from the symbol names index so only
		// the shards the answer touches are decoded.
		stop := tr.Phase("query.load_and_search")
		if refresh {
			seedFromPublishedGraph(ctx, cfg, ws)
		}
		out, g, err = Query(ctx, ws, ws.Root(), cfg, refresh, read.Input, read.Budget, slog.Default())
		stop()
		if err != nil {
			return knowledge.QueryResult{}, err
		}
	} else {
		stop := tr.Phase("query.load_graph")
		g, err = r.LoadGraph(ctx, ws, cfg, refresh, global, seeded)
		stop()
		if err != nil {
			return knowledge.QueryResult{}, err
		}
		stop = tr.Phase("query.search")
		out, err = g.QueryRanked(read.Input, read.Budget, 0, 0, knowledge.RetrieveOptions{Reader: reader, Weights: knowledge.ShippedWeights(), Table: embedtable.Cached})
		stop()
		if err != nil {
			return knowledge.QueryResult{}, err
		}
	}
	if readerErr != nil && out.Ranking != nil {
		out.Ranking.Skipped = append(out.Ranking.Skipped, "reader: "+readerErr.Error())
	}
	return answerQuery(ctx, g, read, out, seeded, r.MeasuredCoverage(ctx, ws, cfg)), nil
}

// SearchLoaded is SearchGraph over g, a graph loaded once for many reads: it must hold
// every symbol shard when read seeds the symbol layer, and only the default graph
// otherwise. table loads the embed table, nil to rank without it; SearchGraph passes
// embedtable.Cached.
func SearchLoaded(ctx context.Context, g *knowledge.Graph, read knowledge.GraphRead, table func() (*embed.Table, error), coverage CoverageFunc) knowledge.QueryResult {
	stop := observability.TraceFromContext(ctx).Phase("query.search")
	out := g.QueryPage(read.Input, read.Budget, 0, 0, table)
	stop()
	return answerQuery(ctx, g, read, out, knowledge.SeedsLazyLayer(read.Input), coverage)
}

// answerQuery judges out, what g matched for read, and names the nearest node when
// nothing matched.
func answerQuery(ctx context.Context, g *knowledge.Graph, read knowledge.GraphRead, out types.KnowledgeQueryOutput, seeded bool, coverage CoverageFunc) knowledge.QueryResult {
	stop := observability.TraceFromContext(ctx).Phase("query.coverage")
	out.Answer = knowledge.Answer(read.Input, out.MatchCount > 0, coverage(read.Input, seeded))
	stop()
	res := knowledge.QueryResult{Out: out}
	if out.MatchCount == 0 && read.Nearest {
		res.Nearest = g.NearestNode(read.Input)
	}
	return res
}

// ExplainNode answers read from the graph ws builds under cfg.
func (r Reads) ExplainNode(ctx context.Context, ws ReadWorkspace, cfg config.Config, read knowledge.GraphRead, refresh, global bool) (knowledge.ExplainResult, error) {
	tr := observability.TraceFromContext(ctx)
	seeded := knowledge.SeedsLazyLayer(read.Input)
	stop := tr.Phase("explain.load_graph")
	g, err := r.LoadGraph(ctx, ws, cfg, refresh, global, seeded)
	stop()
	if err != nil {
		return knowledge.ExplainResult{}, err
	}
	return r.ExplainLoaded(ctx, g, read, r.MeasuredCoverage(ctx, ws, cfg)), nil
}

// ExplainLoaded is ExplainNode over g, a graph loaded once for many reads: it must hold
// every symbol shard when read seeds the symbol layer, and only the default graph
// otherwise.
func (Reads) ExplainLoaded(ctx context.Context, g *knowledge.Graph, read knowledge.GraphRead, coverage CoverageFunc) knowledge.ExplainResult {
	tr := observability.TraceFromContext(ctx)
	seeded := knowledge.SeedsLazyLayer(read.Input)
	stop := tr.Phase("explain.search")
	out, found := g.Explain(read.Input)
	stop()
	// A bare name does not seed symbols, so a miss was resolved against a graph that held
	// no code symbols. Reporting that as "no node matches" is how a real symbol comes to
	// look nonexistent, so the answer says so when the input could have named one, and a
	// typo'd `kind:target` still gets the absent verdict it deserves.
	stop = tr.Phase("explain.coverage")
	cov := coverage(read.Input, seeded)
	if found {
		// One symbol: only its own language's indexes could have missed a site of it.
		cov = cov.Narrow(g.SymbolLanguage(read.Input))
	}
	res := knowledge.ExplainResult{Out: out, Found: found, Answer: knowledge.Answer(read.Input, found, cov)}
	stop()
	if !found {
		res.Nearest = g.NearestNode(read.Input)
	}
	return res
}

// LoadGraph gathers the workspace inputs and runs the cache-first build for a workspace the
// caller already holds, read under cfg: the form a server answering for one of many
// workspaces calls.
func (r Reads) LoadGraph(ctx context.Context, ws ReadWorkspace, cfg config.Config, refresh, global, includeSymbols bool) (*knowledge.Graph, error) {
	if global {
		// Cross-workspace symbol federation is a later phase; --global stays domain-only.
		// Warn rather than silently drop a symbol-seeded selection, so an empty result
		// is not mistaken for "no such symbol".
		if includeSymbols {
			interactive.Hint(ctx, "note: symbol queries are domain-only under --global (cross-workspace symbols are a later phase)")
		}
		full, err := r.Full(ctx, ws)
		if err != nil {
			return nil, err
		}
		return BuildGlobal(ctx, full, cfg, refresh, slog.Default())
	}
	if refresh {
		// Before paying to rebuild, take the published copy if this workspace names one.
		// A refresh is the moment the expensive half (the SCIP shards, which are never
		// committed) would otherwise be recomputed, and a branch switch reaches here
		// through the VCS refresh hook's sync-graph job.
		seedFromPublishedGraph(ctx, cfg, ws)
	}
	g, err := Build(ctx, ws, ws.Root(), cfg, refresh, slog.Default())
	if err != nil {
		return nil, err
	}
	if includeSymbols {
		if err := MergeSymbols(ctx, ws, ws.Root(), cfg, g, slog.Default()); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// seedFromPublishedGraph points the workspace's knowledge store at the published artifact,
// so the rebuild that follows fetches a missing shard instead of recomputing it. It fetches
// nothing itself: the store asks, shard by shard and by fingerprint, for only what it lacks.
//
// It ANNOUNCES ITSELF BEFORE any request, naming the host the build is about to reach. This
// is the only place magus talks to a network the user did not ask it to talk to: it fires
// from a branch switch, through the VCS refresh hook, and an unexplained pause there is
// indistinguishable from a hang. Saying so afterwards is too late to be the explanation,
// and saying it at debug level says it to nobody.
//
// The OUTCOME is best-effort and quiet. No published graph, no network, a private package,
// a tag nobody pushed: each means the graph is built locally, which is what would have
// happened anyway. Only the attempt is loud, because only the attempt costs the user
// something they did not ask for.
//
// It reads the published ref from cfg, not the process configuration, so a server
// answering for another workspace seeds from that workspace's ref.
//
// Does nothing unless knowledge.published_ref names an artifact. Opt-in per repository,
// because reading a graph decides what magus answers about this tree.
func seedFromPublishedGraph(ctx context.Context, cfg config.Config, ws types.Inspector) {
	ref := cfg.Knowledge.PublishedRef
	if ref == "" {
		return
	}
	src, err := knowledge.GraphReference(ref, knowledge.GraphFloatingTag)
	if err != nil {
		// A ref nobody can parse is a misconfiguration, not a quiet miss: the user asked
		// for this pull by writing the key, so the key being wrong is worth their
		// attention even though the build carries on without it.
		interactive.Hint(ctx, fmt.Sprintf("knowledge.published_ref %q does not parse, so no graph was pulled: %v", ref, err))
		return
	}

	slog.InfoContext(ctx, fmt.Sprintf("fetching the published knowledge graph from %s (knowledge.published_ref; unset it to build locally)", src.Registry),
		attr.Notice(""), attr.Component("magus"))
	// The timeout rides on the HTTP client rather than a ctx bounded here, because the
	// requests happen later, inside the build, and a deadline started now would expire
	// against whatever else that build has to do first.
	client := &oci.Client{HTTP: &http.Client{Timeout: knowledge.RegistryTimeout}}
	UsePublishedShards(ws, PublishedShards(client, src, knowledge.GraphArtifactType, slog.Default()))
}
