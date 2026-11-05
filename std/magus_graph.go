//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/embedtable"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/types"
)

// knowledgeGraphs is the slice of the workspace the graph members read. *magus.Magus
// satisfies it, and it is recovered from the workspace on the context by assertion for
// the same reason Analyzer is: std cannot import root magus.
type knowledgeGraphs interface {
	KnowledgeGraph(ctx context.Context, req types.GraphRequest) (*knowledge.Graph, error)
	SymbolGaps(ctx context.Context) ([]types.KnowledgeSymbolGap, bool)
}

func graphsFromContext(ctx context.Context, member string) (knowledgeGraphs, error) { //nolint:ireturn // the workspace backing is chosen at runtime
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return nil, errorNoWorkspace(member)
	}
	g, ok := ws.(knowledgeGraphs)
	if !ok {
		return nil, fmt.Errorf("magus\\%s: this workspace has no knowledge graph", member)
	}
	if memo := types.EvalMemoFromContext(ctx); memo != nil {
		return memoGraphs{g: g, memo: memo}, nil
	}
	return g, nil
}

// memoGraphs answers every graph read of one evaluation from one build.
// So a figure calling magus\graph().dir per box builds the graph once.
type memoGraphs struct {
	g    knowledgeGraphs
	memo *types.EvalMemo
}

// KnowledgeGraph builds once per distinct scope; a refresh always rebuilds.
func (m memoGraphs) KnowledgeGraph(ctx context.Context, req types.GraphRequest) (*knowledge.Graph, error) {
	if req.Refresh {
		return m.g.KnowledgeGraph(ctx, req)
	}
	key := "graph"
	if req.Symbols.Loaded() {
		key = "graph+symbols:" + req.Symbols.Ref()
	}
	return memoGraph(m.memo, key, func() (*knowledge.Graph, error) { return m.g.KnowledgeGraph(ctx, req) })
}

func (m memoGraphs) SymbolGaps(ctx context.Context) ([]types.KnowledgeSymbolGap, bool) {
	type gaps struct {
		list   []types.KnowledgeSymbolGap
		probed bool
	}
	v, _ := m.memo.Do("symbol-gaps", func() (any, error) {
		list, probed := m.g.SymbolGaps(ctx)
		return gaps{list, probed}, nil
	})
	got, _ := v.(gaps)
	return got.list, got.probed
}

func memoGraph(memo *types.EvalMemo, key string, build func() (*knowledge.Graph, error)) (*knowledge.Graph, error) {
	v, err := memo.Do(key, func() (any, error) { return build() })
	if err != nil {
		return nil, err
	}
	g, _ := v.(*knowledge.Graph)
	return g, nil
}

// graphCoverage reports what a lookup could consult, for knowledge.Answer to judge. The
// gap probe stats one file per declared index, so it runs only when the input could
// have named a code symbol.
func graphCoverage(ctx context.Context, g knowledgeGraphs, input string, seeded bool) knowledge.Coverage {
	cov := knowledge.Coverage{Seeded: seeded}
	if knowledge.CouldMatchLazyLayer(input) {
		cov.Gaps, cov.Probed = g.SymbolGaps(ctx)
	}
	return cov
}

// MagusQuery backs magus\graph().query: ranked matches plus their neighborhood.
// It is the record `magus query -o json` prints.
// A query seeding the symbol layer reads the symbol shards too.
func MagusQuery(ctx context.Context, query string, opts types.KnowledgeQueryOptions) (types.KnowledgeQueryOutput, error) {
	if strings.TrimSpace(query) == "" {
		return types.KnowledgeQueryOutput{}, errors.New("magus\\graph().query: needs search terms, such as \"kind=spell go\"")
	}
	if err := nonNegative("query", map[string]int{"budget": opts.Budget, "limit": opts.Limit, "offset": opts.Offset}); err != nil {
		return types.KnowledgeQueryOutput{}, err
	}
	g, err := graphsFromContext(ctx, "query")
	if err != nil {
		return types.KnowledgeQueryOutput{}, err
	}
	seeded := knowledge.SeedsLazyLayer(query)
	kg, err := loadGraph(ctx, g, seeded)
	if err != nil {
		return types.KnowledgeQueryOutput{}, err
	}
	reader, readerErr := workspacegraph.ReaderContext(ctx, types.WorkspaceFromContext(ctx), kg.Root())
	out, err := kg.QueryRanked(query, opts.Budget, opts.Offset, opts.Limit, knowledge.RetrieveOptions{Reader: reader, Weights: knowledge.ShippedWeights(), Table: embedtable.Cached})
	if err != nil {
		return types.KnowledgeQueryOutput{}, err
	}
	if readerErr != nil {
		out.Ranking.Skipped = append(out.Ranking.Skipped, "reader: "+readerErr.Error())
	}
	// The verdict judges the whole match set, not the page, so every page states the
	// same coverage.
	out.Answer = knowledge.Answer(query, out.MatchCount > 0, graphCoverage(ctx, g, query, seeded))
	return out, nil
}

// MagusExplain backs magus\graph().explain: one node's context card.
// It is the record `magus explain -o json` prints.
// A path between two nodes is magus\graph().path.
func MagusExplain(ctx context.Context, node string) (types.KnowledgeExplainOutput, error) {
	if node == "" {
		return types.KnowledgeExplainOutput{}, errors.New("magus\\graph().explain: needs a node ID or a name that resolves to one")
	}
	g, err := graphsFromContext(ctx, "explain")
	if err != nil {
		return types.KnowledgeExplainOutput{}, err
	}
	seeded := knowledge.SeedsLazyLayer(node)
	kg, err := loadGraph(ctx, g, seeded)
	if err != nil {
		return types.KnowledgeExplainOutput{}, err
	}
	out, ok := kg.Explain(node)
	if !ok {
		// A bare name reads the symbol-free graph, so a miss on a name that could be a
		// code symbol is a blind spot, not an absence.
		if knowledge.Answer(node, false, graphCoverage(ctx, g, node, seeded)).Verdict == types.VerdictUnknown {
			return types.KnowledgeExplainOutput{}, fmt.Errorf("magus\\graph().explain: no node matches %q in the domain graph, code symbols are not loaded here, so ask magus\\graph().refs", node)
		}
		return types.KnowledgeExplainOutput{}, fmt.Errorf("magus\\graph().explain: no node matches %q", node)
	}
	return out, nil
}

// loadGraph reads the domain graph, with the symbol shards merged when seeded.
func loadGraph(ctx context.Context, g knowledgeGraphs, seeded bool) (*knowledge.Graph, error) {
	var req types.GraphRequest
	if seeded {
		req.Symbols = types.AllSymbols
	}
	return g.KnowledgeGraph(ctx, req)
}

// MagusPath backs magus\graph().path: the shortest chain from node to to.
// It is the record `magus path -o json` prints.
// opts is a PathOptions; relations narrows the hops.
// A resolved pair with no connection is an answer (found is false); only an endpoint
// that resolves to nothing, or an option that names nothing, raises.
func MagusPath(ctx context.Context, node, to string, opts types.KnowledgePathOptions) (types.KnowledgePathOutput, error) {
	if node == "" || to == "" {
		return types.KnowledgePathOutput{}, errors.New("magus\\graph().path: needs both endpoints")
	}
	g, err := graphsFromContext(ctx, "path")
	if err != nil {
		return types.KnowledgePathOutput{}, err
	}
	kg, err := loadGraph(ctx, g, knowledge.SeedsLazyLayer(node) || knowledge.SeedsLazyLayer(to))
	if err != nil {
		return types.KnowledgePathOutput{}, err
	}
	out, ok := kg.PathWith(node, to, opts)
	if !ok {
		return types.KnowledgePathOutput{}, fmt.Errorf("magus\\graph().path: could not resolve %q or %q to a node", node, to)
	}
	return out, nil
}

// MagusRefs backs magus\graph().refs: a symbol's definition and its users.
// It is the record `magus refs -o json` prints.
// A symbol nothing defines is an
// answer carrying a verdict, never a raise: absent and unknown are different facts and a
// caller branches on answer.verdict to tell them apart.
func MagusRefs(ctx context.Context, symbol string, opts types.KnowledgeRefsOptions) (types.KnowledgeRefsOutput, error) {
	if symbol == "" {
		return types.KnowledgeRefsOutput{}, errors.New("magus\\graph().refs: needs a symbol ID or a name that resolves to one")
	}
	if err := nonNegative("refs", map[string]int{"limit": opts.Limit, "offset": opts.Offset}); err != nil {
		return types.KnowledgeRefsOutput{}, err
	}
	g, err := graphsFromContext(ctx, "refs")
	if err != nil {
		return types.KnowledgeRefsOutput{}, err
	}
	kg, err := g.KnowledgeGraph(ctx, types.GraphRequest{Symbols: types.SymbolsMentioning(symbol)})
	if err != nil {
		return types.KnowledgeRefsOutput{}, err
	}
	out, ok := kg.Refs(symbol)
	if !ok {
		out = types.KnowledgeRefsOutput{
			Definition:    types.KnowledgeRefsDefinition,
			SchemaVersion: types.KnowledgeSchemaVersion,
			Symbol:        symbol,
		}
	}
	out.Answer = knowledge.Answer(symbol, len(out.Refs) > 0, graphCoverage(ctx, g, symbol, true))
	// fileCount and refCount keep describing the whole set; only refs is windowed.
	if opts.Offset >= len(out.Refs) {
		out.Refs = nil
	} else {
		out.Refs = out.Refs[opts.Offset:]
	}
	if opts.Limit > 0 && len(out.Refs) > opts.Limit {
		out.Refs = out.Refs[:opts.Limit]
	}
	return out, nil
}

// MagusStats backs magus\graph().stats: the shape `magus graph stats` prints.
// kind scopes every section to one node kind; empty is the whole graph.
func MagusStats(ctx context.Context, kind string) (types.KnowledgeStats, error) {
	g, err := graphsFromContext(ctx, "stats")
	if err != nil {
		return types.KnowledgeStats{}, err
	}
	kg, err := g.KnowledgeGraph(ctx, types.GraphRequest{})
	if err != nil {
		return types.KnowledgeStats{}, err
	}
	return kg.Stats(kind), nil
}

// MagusImportGraph backs magus\graph().imports(): which package imports which.
// Indexed says whether a symbol index was there to read at all.
func MagusImportGraph(ctx context.Context) (types.ImportGraph, error) {
	g, err := graphsFromContext(ctx, "importGraph")
	if err != nil {
		return types.ImportGraph{}, err
	}
	kg, err := g.KnowledgeGraph(ctx, types.GraphRequest{Symbols: types.AllSymbols})
	if err != nil {
		return types.ImportGraph{}, err
	}
	return kg.ImportGraph(), nil
}

// precedentWorkspace is the part of *magus.Magus the graph's precedents read.
// That is an index freshen, `magus status`'s verdict and the declared outputs.
// knowledge.conformance's gates and ceilings judge the precedents.
type precedentWorkspace interface {
	FreshenSymbolIndexes(ctx context.Context, languages []string) error
	SymbolIndexStatusByStamp(ctx context.Context) []types.SymbolIndexStatus
	ClassifyFiles(ctx context.Context, paths []string) ([]types.FileEntry, error)
	ConformanceOptions() (types.ConformanceOptions, error)
}

// indexedGraph is what a member judging the symbol indexes reads: the workspace, the graph
// with its symbols, each declared index's freshness, and the files that are declared outputs.
type indexedGraph struct {
	ws        precedentWorkspace
	kg        *knowledge.Graph
	indexes   []types.SymbolIndexStatus
	generated map[string]bool
}

// judgedGraph freshens the indexes of languages, every index when empty.
// An index it cannot make current reads as stale, with the freshen's error.
// Freshness is judged first, so it covers the index the answer came from.
func judgedGraph(ctx context.Context, member string, languages []string) (indexedGraph, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return indexedGraph{}, errorNoWorkspace(member)
	}
	pw, ok := ws.(precedentWorkspace)
	if !ok {
		return indexedGraph{}, fmt.Errorf("magus\\%s: this workspace cannot judge its symbol indexes", member)
	}
	g, err := graphsFromContext(ctx, member)
	if err != nil {
		return indexedGraph{}, err
	}
	freshErr := pw.FreshenSymbolIndexes(ctx, languages)
	indexes := pw.SymbolIndexStatusByStamp(ctx)
	if freshErr != nil {
		for i := range indexes {
			if idx := &indexes[i]; idx.Freshness != types.SymbolIndexFresh {
				idx.Detail = strings.TrimSpace(fmt.Sprintf(`%s
%s`, idx.Detail, freshErr.Error()))
			}
		}
	}
	kg, err := g.KnowledgeGraph(ctx, types.GraphRequest{Symbols: types.AllSymbols})
	if err != nil {
		return indexedGraph{}, err
	}
	var files []string
	for _, n := range kg.Nodes() {
		if n.Kind == types.KindFile {
			files = append(files, n.Source)
		}
	}
	slices.Sort(files)
	entries, err := pw.ClassifyFiles(ctx, slices.Compact(files))
	if err != nil {
		return indexedGraph{}, fmt.Errorf("magus\\%s: %w", member, err)
	}
	generated := map[string]bool{}
	for _, e := range entries {
		if e.Role == types.DiffRoleOutput {
			generated[e.Path] = true
		}
	}
	return indexedGraph{ws: pw, kg: kg, indexes: indexes, generated: generated}, nil
}

// MagusPrecedents backs magus\graph().precedents from the symbol indexes.
// knowledge.conformance's gates and ceilings apply.
// Each index's freshness is judged as judgedGraph describes.
func MagusPrecedents(ctx context.Context, languages []string) (types.Holds, error) {
	ig, err := judgedGraph(ctx, "Graph.precedents", languages)
	if err != nil {
		return types.Holds{}, err
	}
	conformance, err := ig.ws.ConformanceOptions()
	if err != nil {
		return types.Holds{}, fmt.Errorf("magus\\Graph.precedents: %w", err)
	}
	conformance.Generated = ig.generated
	return types.Holds{
		Holds:   ig.kg.Precedents(knowledge.PrecedentOptions{Generated: ig.generated, Conformance: conformance}),
		Indexes: ig.indexes,
	}, nil
}

// ceilingWorkspace is the part of *magus.Magus that judges a change against its base revision.
type ceilingWorkspace interface {
	ConformanceCeilings(ctx context.Context, c types.Ceiling, head types.Conformance) (types.Conformance, error)
	MeasureConformanceBase(ctx context.Context, base string) (string, error)
}

// MagusConformanceBase backs magus\change(base).mergeBase().
// It measures the merge base under this workspace's configuration.
// It keeps the measure for Change.conformance and returns the commit.
// Run it from a target, so the indexing is declared work.
func MagusConformanceBase(ctx context.Context, base string) (string, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return "", errorNoWorkspace("conformanceBase")
	}
	cw, ok := ws.(ceilingWorkspace)
	if !ok {
		return "", errors.New("magus\\change().mergeBase: this workspace cannot measure a base revision")
	}
	commit, err := cw.MeasureConformanceBase(ctx, base)
	if err != nil {
		return "", fmt.Errorf("magus\\change().mergeBase: %w", err)
	}
	return commit, nil
}

// MagusConformance backs magus\change().conformance over the symbol indexes.
// Each index's freshness is judged as judgedGraph describes.
// Generated files are never counted. With a base it adds the ceilings checks crossed since
// it (ConformanceCeilings). It writes nothing in the workspace. The answer is a
// Conformance with an indexes list, as a map.
func MagusConformance(ctx context.Context, checks []string, base string) (types.ConformanceReport, error) {
	c := types.Ceiling{Base: base}
	for _, m := range checks {
		c.Checks = append(c.Checks, types.ConformanceMetric(m))
	}
	if err := c.Validate(); err != nil {
		return types.ConformanceReport{}, fmt.Errorf("magus\\Change.conformance: %w", err)
	}
	ig, err := judgedGraph(ctx, "Change.conformance", nil)
	if err != nil {
		return types.ConformanceReport{}, err
	}
	opts, err := ig.ws.ConformanceOptions()
	if err != nil {
		return types.ConformanceReport{}, fmt.Errorf("magus\\Change.conformance: %w", err)
	}
	opts.Generated = ig.generated
	out := ig.kg.Conformance(opts)
	out.Definition = types.ConformanceDefinition
	if out, err = withCeilings(ctx, ig.ws, c, out); err != nil {
		return types.ConformanceReport{}, err
	}
	return types.ConformanceReport{Conformance: out, Indexes: ig.indexes}, nil
}

// withCeilings adds the ceilings c crossed since its base; no base adds none.
func withCeilings(ctx context.Context, ws any, c types.Ceiling, out types.Conformance) (types.Conformance, error) {
	if c.Base == "" {
		return out, nil
	}
	cw, ok := ws.(ceilingWorkspace)
	if !ok {
		return out, errors.New("magus\\Change.conformance: this workspace cannot measure a base revision")
	}
	out, err := cw.ConformanceCeilings(ctx, c, out)
	if err != nil {
		return out, fmt.Errorf("magus\\Change.conformance: %w", err)
	}
	return out, nil
}

// MagusSymbols backs magus\graph().symbols: every declaration with its doc.
// Each index's freshness is judged as judgedGraph describes. Test and
// generated files are never listed. The answer is a Symbols as a map.
func MagusSymbols(ctx context.Context, languages []string) (types.Symbols, error) {
	ig, err := judgedGraph(ctx, "Graph.symbols", languages)
	if err != nil {
		return types.Symbols{}, err
	}
	decls := ig.kg.SymbolDecls(knowledge.SymbolDeclOptions{Generated: ig.generated})
	if decls == nil {
		// No symbols reads as an empty list, never null.
		decls = []types.SymbolDecl{}
	}
	return types.Symbols{Symbols: decls, Indexes: ig.indexes}, nil
}

// MagusDir backs magus\graph().dir: one workspace directory as a Dir.
// dir nodes for Go packages
// live in the symbol shards, so this reads the graph with them merged. A path with no dir
// node raises DirNotInGraph: a typo must not come back as an empty Dir.
func MagusDir(ctx context.Context, dir string) (types.Dir, error) {
	if strings.TrimSpace(dir) == "" {
		return types.Dir{}, errors.New("magus\\graph().dir: needs a workspace-relative directory, such as \"internal/httpx\"")
	}
	g, err := graphsFromContext(ctx, "dir")
	if err != nil {
		return types.Dir{}, err
	}
	kg, err := g.KnowledgeGraph(ctx, types.GraphRequest{Symbols: types.AllSymbols})
	if err != nil {
		return types.Dir{}, err
	}
	d, err := kg.Dir(dir, workspaceLayers(ctx))
	if err != nil {
		return types.Dir{}, fmt.Errorf("magus\\graph().dir: %w", err)
	}
	return d, nil
}

// MagusDirs backs magus\graph().dirs: every dir matching glob, sorted by path.
// opts is a DirsOptions; a layer it names that nothing declares raises
// LayerNotDeclared.
func MagusDirs(ctx context.Context, glob string, opts types.DirsOptions) ([]types.Dir, error) {
	if strings.TrimSpace(glob) == "" {
		return nil, errors.New("magus\\graph().dirs: needs a glob over workspace paths, such as \"internal/**\"")
	}
	if err := nonNegative("dirs", map[string]int{"depth": opts.Depth}); err != nil {
		return nil, err
	}
	g, err := graphsFromContext(ctx, "dirs")
	if err != nil {
		return nil, err
	}
	kg, err := g.KnowledgeGraph(ctx, types.GraphRequest{Symbols: types.AllSymbols})
	if err != nil {
		return nil, err
	}
	dirs, err := kg.Dirs(glob, opts, workspaceLayers(ctx))
	if err != nil {
		return nil, fmt.Errorf("magus\\graph().dirs: %w", err)
	}
	return dirs, nil
}

// MagusLayer backs magus\graph().layer: a declared layer and every dir in it.
// A name no
// magus.project "layers" entry declares raises LayerNotDeclared.
func MagusLayer(ctx context.Context, name string) (types.Layer, error) {
	if strings.TrimSpace(name) == "" {
		return types.Layer{}, errors.New("magus\\graph().layer: needs a layer name, such as \"handler\"")
	}
	g, err := graphsFromContext(ctx, "layer")
	if err != nil {
		return types.Layer{}, err
	}
	kg, err := g.KnowledgeGraph(ctx, types.GraphRequest{Symbols: types.AllSymbols})
	if err != nil {
		return types.Layer{}, err
	}
	l, err := kg.Layer(name, workspaceLayers(ctx))
	if err != nil {
		return types.Layer{}, fmt.Errorf("magus\\graph().layer: %w", err)
	}
	return l, nil
}

// MagusNeighborhood backs magus\graph().neighborhood around focus.
// opts is a NeighborhoodOptions: depth, relations, direction and collapse.
func MagusNeighborhood(ctx context.Context, focus string, opts types.KnowledgeNeighborhoodOptions) (types.KnowledgeNeighborhoodOutput, error) {
	if strings.TrimSpace(focus) == "" {
		return types.KnowledgeNeighborhoodOutput{}, errors.New("magus\\graph().neighborhood: needs a focus node ID, path or name")
	}
	if err := nonNegative("neighborhood", map[string]int{"depth": opts.Depth}); err != nil {
		return types.KnowledgeNeighborhoodOutput{}, err
	}
	no := opts
	g, err := graphsFromContext(ctx, "neighborhood")
	if err != nil {
		return types.KnowledgeNeighborhoodOutput{}, err
	}
	kg, err := g.KnowledgeGraph(ctx, types.GraphRequest{Symbols: types.AllSymbols})
	if err != nil {
		return types.KnowledgeNeighborhoodOutput{}, err
	}
	out, ok := kg.FocusNeighborhood(focus, no)
	if !ok {
		return types.KnowledgeNeighborhoodOutput{}, fmt.Errorf("magus\\graph().neighborhood: no node matches %q", focus)
	}
	matched := len(out.Nodes) > 1
	if !walksSymbolLayer(no.Relations) {
		out.Answer = types.ClassifyAnswer(matched, "", nil)
		return out, nil
	}
	// The shards were merged, but a graph holding no symbol read no index at all, and a
	// thin imports walk over it is unknown rather than absent.
	cov := knowledge.Coverage{Seeded: kg.HasSymbols()}
	cov.Gaps, cov.Probed = g.SymbolGaps(ctx)
	// The walk, not a query string, decides relevance; "" leaves the layer in scope.
	out.Answer = knowledge.Answer("", matched, cov)
	return out, nil
}

// symbolLayerRelations are the relations whose edges the symbol shards hold.
var symbolLayerRelations = []types.RelationID{
	types.RelationImports, types.RelationDefines, types.RelationReferences, types.RelationCalls, types.RelationContains,
	types.RelationImplements,
}

func walksSymbolLayer(rels []types.RelationID) bool {
	return len(rels) == 0 || slices.ContainsFunc(rels, func(r types.RelationID) bool {
		return slices.Contains(symbolLayerRelations, r)
	})
}

// workspaceLayers merges every project's magus.project "layers" declarations. Their keys
// are workspace-relative, so one map holds them all.
func workspaceLayers(ctx context.Context) map[string]string {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return nil
	}
	layers := map[string]string{}
	for _, p := range ws.All() {
		maps.Copy(layers, p.Layers)
	}
	return layers
}

// MagusOutput backs magus\output: one target run's captured output by its ref, the bytes
// `magus query output <ref>` prints, with the run's identity beside them. It reads this
// checkout's output store, so a ref minted in another worktree does not resolve here.
// Inside a guard rule there is no workspace, and the store is the one the guard pinned for
// the checkout it judges.
func MagusOutput(ctx context.Context, ref string) (types.OutputRecord, error) {
	if !cache.LooksLikeRef(ref) {
		return types.OutputRecord{}, fmt.Errorf("magus\\output: %q is not an output ref (expected out<hex>, such as out1a2b3c)", ref)
	}
	cacheDir, err := checkoutCacheDir(ctx, "output")
	if err != nil {
		return types.OutputRecord{}, err
	}
	data, desc, err := cache.NewOutputStore(cacheDir).ByRef(ref)
	if errors.Is(err, fs.ErrNotExist) {
		return types.OutputRecord{}, types.DiagnosticErrorf(types.OutputRefMissing, "magus\\output: no stored output for ref %q in this checkout", ref)
	}
	if err != nil {
		return types.OutputRecord{}, fmt.Errorf("magus\\output: %w", err)
	}
	return types.OutputRecord{
		Ref:        desc.Ref,
		Project:    desc.Project,
		Target:     desc.Target,
		Failed:     desc.Failed,
		DurationMs: desc.DurationMs,
		Output:     string(data),
	}, nil
}

// nonNegative refuses a negative window option, the first in name order.
func nonNegative(member string, opts map[string]int) error {
	for _, k := range slices.Sorted(maps.Keys(opts)) {
		if opts[k] < 0 {
			return fmt.Errorf("magus\\%s: %s must not be negative, got %d", member, k, opts[k])
		}
	}
	return nil
}
