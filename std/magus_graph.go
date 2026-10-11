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
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// knowledgeGraphs is the slice of the workspace the graph members read. *magus.Magus
// satisfies it, and it is recovered from the workspace on the context by assertion for
// the same reason Analyzer is: std cannot import root magus.
type knowledgeGraphs interface {
	KnowledgeGraph(ctx context.Context, refresh bool) (*knowledge.Graph, error)
	KnowledgeGraphWithSymbols(ctx context.Context) (*knowledge.Graph, error)
	KnowledgeGraphWithSymbolsForRef(ctx context.Context, symbol string) (*knowledge.Graph, error)
	SymbolGaps(ctx context.Context) ([]types.KnowledgeSymbolGap, bool)
}

func graphsFromContext(ctx context.Context, member string) (knowledgeGraphs, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return nil, errNoWorkspace(member)
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

// memoGraphs answers every graph read of one evaluation from one build, so a figure
// calling magus\dir per box builds the graph once.
type memoGraphs struct {
	g    knowledgeGraphs
	memo *types.EvalMemo
}

func (m memoGraphs) KnowledgeGraph(ctx context.Context, refresh bool) (*knowledge.Graph, error) {
	if refresh {
		return m.g.KnowledgeGraph(ctx, true)
	}
	return memoGraph(m.memo, "graph", func() (*knowledge.Graph, error) { return m.g.KnowledgeGraph(ctx, false) })
}

func (m memoGraphs) KnowledgeGraphWithSymbols(ctx context.Context) (*knowledge.Graph, error) {
	return memoGraph(m.memo, "graph+symbols", func() (*knowledge.Graph, error) { return m.g.KnowledgeGraphWithSymbols(ctx) })
}

// KnowledgeGraphWithSymbolsForRef merges only the shards mentioning symbol, a subset of
// every shard, so it reuses the full merge rather than caching one graph per symbol.
func (m memoGraphs) KnowledgeGraphWithSymbolsForRef(ctx context.Context, _ string) (*knowledge.Graph, error) {
	return m.KnowledgeGraphWithSymbols(ctx)
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

// MagusQuery backs magus\query: ranked matches for query plus their neighborhood, the
// record `magus query -o json` prints. A query that seeds the symbol layer reads a graph
// with the symbol shards merged in; every other query reads the domain graph.
func MagusQuery(ctx context.Context, query string, opts map[string]any) (types.KnowledgeQueryOutput, error) {
	if strings.TrimSpace(query) == "" {
		return types.KnowledgeQueryOutput{}, errors.New("magus\\query: needs search terms, such as \"kind=spell go\"")
	}
	o, err := intOptions("query", opts, "budget", "limit", "offset")
	if err != nil {
		return types.KnowledgeQueryOutput{}, err
	}
	g, err := graphsFromContext(ctx, "query")
	if err != nil {
		return types.KnowledgeQueryOutput{}, err
	}
	seeded := knowledge.SeedsLazyLayer(query)
	var kg *knowledge.Graph
	if seeded {
		kg, err = g.KnowledgeGraphWithSymbols(ctx)
	} else {
		kg, err = g.KnowledgeGraph(ctx, false)
	}
	if err != nil {
		return types.KnowledgeQueryOutput{}, err
	}
	out := kg.QueryPage(query, o["budget"], o["offset"], o["limit"])
	// The verdict judges the whole match set, not the page, so every page states the
	// same coverage.
	out.Answer = knowledge.Answer(query, out.MatchCount > 0, graphCoverage(ctx, g, query, seeded))
	return out, nil
}

// MagusExplain backs magus\explain: one node's context card, the record `magus explain
// -o json` prints. A path between two nodes is magus\path.
func MagusExplain(ctx context.Context, node string) (types.KnowledgeExplainOutput, error) {
	if node == "" {
		return types.KnowledgeExplainOutput{}, errors.New("magus\\explain: needs a node ID or a name that resolves to one")
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
			return types.KnowledgeExplainOutput{}, fmt.Errorf("magus\\explain: no node matches %q in the domain graph, code symbols are not loaded here, so ask magus\\refs", node)
		}
		return types.KnowledgeExplainOutput{}, fmt.Errorf("magus\\explain: no node matches %q", node)
	}
	return out, nil
}

// loadGraph reads the domain graph, with the symbol shards merged when seeded.
func loadGraph(ctx context.Context, g knowledgeGraphs, seeded bool) (*knowledge.Graph, error) {
	if seeded {
		return g.KnowledgeGraphWithSymbols(ctx)
	}
	return g.KnowledgeGraph(ctx, false)
}

// MagusPath backs magus\path: the shortest chain of edges between node and to, the
// record `magus path -o json` prints. opts is a PathOptions; relations narrows the hops.
// A resolved pair with no connection is an answer (found is false); only an endpoint
// that resolves to nothing, or an option that names nothing, raises.
func MagusPath(ctx context.Context, node, to string, opts map[string]any) (types.KnowledgePathOutput, error) {
	if node == "" || to == "" {
		return types.KnowledgePathOutput{}, errors.New("magus\\path: needs both endpoints")
	}
	o := optionReader{member: "path", opts: opts}
	if err := o.only("relations"); err != nil {
		return types.KnowledgePathOutput{}, err
	}
	rels, err := o.relations()
	if err != nil {
		return types.KnowledgePathOutput{}, err
	}
	g, err := graphsFromContext(ctx, "path")
	if err != nil {
		return types.KnowledgePathOutput{}, err
	}
	kg, err := loadGraph(ctx, g, knowledge.SeedsLazyLayer(node) || knowledge.SeedsLazyLayer(to))
	if err != nil {
		return types.KnowledgePathOutput{}, err
	}
	out, ok := kg.PathWith(node, to, types.KnowledgePathOptions{Relations: rels})
	if !ok {
		return types.KnowledgePathOutput{}, fmt.Errorf("magus\\path: could not resolve %q or %q to a node", node, to)
	}
	return out, nil
}

// MagusRefs backs magus\refs: where a code symbol is defined and every file that
// references it, the record `magus refs -o json` prints. A symbol nothing defines is an
// answer carrying a verdict, never a raise: absent and unknown are different facts and a
// caller branches on answer.verdict to tell them apart.
func MagusRefs(ctx context.Context, symbol string, opts map[string]any) (types.KnowledgeRefsOutput, error) {
	if symbol == "" {
		return types.KnowledgeRefsOutput{}, errors.New("magus\\refs: needs a symbol ID or a name that resolves to one")
	}
	o, err := intOptions("refs", opts, "limit", "offset")
	if err != nil {
		return types.KnowledgeRefsOutput{}, err
	}
	g, err := graphsFromContext(ctx, "refs")
	if err != nil {
		return types.KnowledgeRefsOutput{}, err
	}
	kg, err := g.KnowledgeGraphWithSymbolsForRef(ctx, symbol)
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
	if o["offset"] >= len(out.Refs) {
		out.Refs = nil
	} else {
		out.Refs = out.Refs[o["offset"]:]
	}
	if o["limit"] > 0 && len(out.Refs) > o["limit"] {
		out.Refs = out.Refs[:o["limit"]]
	}
	return out, nil
}

// MagusStats backs magus\stats: the knowledge graph's shape, the record `magus graph
// stats -o json` prints. kind scopes every section to one node kind; empty is the whole
// graph.
func MagusStats(ctx context.Context, kind string) (types.KnowledgeStats, error) {
	g, err := graphsFromContext(ctx, "stats")
	if err != nil {
		return types.KnowledgeStats{}, err
	}
	kg, err := g.KnowledgeGraph(ctx, false)
	if err != nil {
		return types.KnowledgeStats{}, err
	}
	return kg.Stats(kind), nil
}

// MagusImportGraph backs magus\importGraph: which workspace package imports which, by
// directory, with Indexed saying whether a symbol index was there to read at all.
func MagusImportGraph(ctx context.Context) (types.ImportGraph, error) {
	g, err := graphsFromContext(ctx, "importGraph")
	if err != nil {
		return types.ImportGraph{}, err
	}
	kg, err := g.KnowledgeGraphWithSymbols(ctx)
	if err != nil {
		return types.ImportGraph{}, err
	}
	return kg.ImportGraph(), nil
}

// precedentWorkspace is the part of *magus.Magus magus\precedents and magus\symbols read beside
// the graph: a freshen of every index, the freshness verdict `magus status` prints, and which
// files are declared outputs.
type precedentWorkspace interface {
	FreshenSymbolIndexes(ctx context.Context) error
	SymbolIndexStatusByStamp(ctx context.Context) []types.SymbolIndexStatus
	ClassifyFiles(ctx context.Context, paths []string) ([]types.FileEntry, error)
}

// indexedGraph is what a member judging the symbol indexes reads: the graph with its symbols,
// each declared index's freshness, and the files that are declared outputs.
type indexedGraph struct {
	kg        *knowledge.Graph
	indexes   []types.SymbolIndexStatus
	generated map[string]bool
}

// judgedGraph freshens every index, as magus\diff does, so a caller never runs a graph build of
// its own; one that could not be made current reads as stale, the freshen's error in its
// detail. Freshness is judged before the graph is read, so the verdict covers the index the
// answer came from.
func judgedGraph(ctx context.Context, member string) (indexedGraph, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return indexedGraph{}, errNoWorkspace(member)
	}
	pw, ok := ws.(precedentWorkspace)
	if !ok {
		return indexedGraph{}, fmt.Errorf("magus\\%s: this workspace cannot judge its symbol indexes", member)
	}
	g, err := graphsFromContext(ctx, member)
	if err != nil {
		return indexedGraph{}, err
	}
	freshErr := pw.FreshenSymbolIndexes(ctx)
	indexes := pw.SymbolIndexStatusByStamp(ctx)
	if freshErr != nil {
		for i := range indexes {
			if idx := &indexes[i]; idx.Freshness != types.SymbolIndexFresh {
				idx.Detail = strings.TrimSpace(idx.Detail + "\n" + freshErr.Error())
			}
		}
	}
	kg, err := g.KnowledgeGraphWithSymbols(ctx)
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
	return indexedGraph{kg: kg, indexes: indexes, generated: generated}, nil
}

// reportMap is report as the map a Buzz caller reads, keyed as its JSON is: no Buzz object
// mirrors it.
func reportMap(report any) (map[string]any, error) {
	b, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// MagusPrecedents backs magus\precedents: the precedents the merged symbol indexes establish,
// and each declared index's freshness, judged as judgedGraph describes. Generated files are
// never counted. The answer is a PrecedentReport as a map.
func MagusPrecedents(ctx context.Context) (map[string]any, error) {
	ig, err := judgedGraph(ctx, "precedents")
	if err != nil {
		return nil, err
	}
	return reportMap(types.PrecedentReport{
		Precedents: ig.kg.Precedents(knowledge.PrecedentOptions{Generated: ig.generated}),
		Indexes:    ig.indexes,
	})
}

// MagusSymbols backs magus\symbols: every declaration in the merged symbol indexes with its
// doc comment, and each declared index's freshness, judged as judgedGraph describes. Test and
// generated files are never listed. The answer is a SymbolReport as a map.
func MagusSymbols(ctx context.Context) (map[string]any, error) {
	ig, err := judgedGraph(ctx, "symbols")
	if err != nil {
		return nil, err
	}
	decls := ig.kg.SymbolDecls(knowledge.SymbolDeclOptions{Generated: ig.generated})
	if decls == nil {
		// No symbols reads as an empty list, never null.
		decls = []types.SymbolDecl{}
	}
	return reportMap(types.SymbolReport{Symbols: decls, Indexes: ig.indexes})
}

// MagusDir backs magus\dir: one workspace directory as a Dir. dir nodes for Go packages
// live in the symbol shards, so this reads the graph with them merged. A path with no dir
// node raises DirNotInGraph: a typo must not come back as an empty Dir.
func MagusDir(ctx context.Context, dir string) (types.Dir, error) {
	if strings.TrimSpace(dir) == "" {
		return types.Dir{}, errors.New("magus\\dir: needs a workspace-relative directory, such as \"internal/httpx\"")
	}
	g, err := graphsFromContext(ctx, "dir")
	if err != nil {
		return types.Dir{}, err
	}
	kg, err := g.KnowledgeGraphWithSymbols(ctx)
	if err != nil {
		return types.Dir{}, err
	}
	d, err := kg.Dir(dir, workspaceLayers(ctx))
	if err != nil {
		return types.Dir{}, fmt.Errorf("magus\\dir: %w", err)
	}
	return d, nil
}

// MagusDirs backs magus\dirs: every dir whose workspace path matches glob, sorted by
// path. opts is a DirsOptions; a layer it names that nothing declares raises
// LayerNotDeclared.
func MagusDirs(ctx context.Context, glob string, opts map[string]any) ([]types.Dir, error) {
	if strings.TrimSpace(glob) == "" {
		return nil, errors.New("magus\\dirs: needs a glob over workspace paths, such as \"internal/**\"")
	}
	o := optionReader{member: "dirs", opts: opts}
	if err := o.only("layer", "language", "depth"); err != nil {
		return nil, err
	}
	var do types.DirsOptions
	var err error
	if do.Layer, err = o.str("layer"); err != nil {
		return nil, err
	}
	if do.Language, err = o.str("language"); err != nil {
		return nil, err
	}
	if do.Depth, err = o.count("depth"); err != nil {
		return nil, err
	}
	g, err := graphsFromContext(ctx, "dirs")
	if err != nil {
		return nil, err
	}
	kg, err := g.KnowledgeGraphWithSymbols(ctx)
	if err != nil {
		return nil, err
	}
	dirs, err := kg.Dirs(glob, do, workspaceLayers(ctx))
	if err != nil {
		return nil, fmt.Errorf("magus\\dirs: %w", err)
	}
	return dirs, nil
}

// MagusLayer backs magus\layer: a declared layer and every dir it covers. A name no
// magus.project "layers" entry declares raises LayerNotDeclared.
func MagusLayer(ctx context.Context, name string) (types.Layer, error) {
	if strings.TrimSpace(name) == "" {
		return types.Layer{}, errors.New("magus\\layer: needs a layer name, such as \"handler\"")
	}
	g, err := graphsFromContext(ctx, "layer")
	if err != nil {
		return types.Layer{}, err
	}
	kg, err := g.KnowledgeGraphWithSymbols(ctx)
	if err != nil {
		return types.Layer{}, err
	}
	l, err := kg.Layer(name, workspaceLayers(ctx))
	if err != nil {
		return types.Layer{}, fmt.Errorf("magus\\layer: %w", err)
	}
	return l, nil
}

// MagusNeighborhood backs magus\neighborhood: the subgraph within opts.depth hops of
// focus, along opts.relations in opts.direction, folded by opts.collapse. opts is a
// NeighborhoodOptions.
func MagusNeighborhood(ctx context.Context, focus string, opts map[string]any) (types.KnowledgeNeighborhoodOutput, error) {
	if strings.TrimSpace(focus) == "" {
		return types.KnowledgeNeighborhoodOutput{}, errors.New("magus\\neighborhood: needs a focus node ID, path or name")
	}
	o := optionReader{member: "neighborhood", opts: opts}
	if err := o.only("depth", "relations", "direction", "collapse"); err != nil {
		return types.KnowledgeNeighborhoodOutput{}, err
	}
	var no types.KnowledgeNeighborhoodOptions
	var err error
	if no.Depth, err = o.count("depth"); err != nil {
		return types.KnowledgeNeighborhoodOutput{}, err
	}
	if no.Relations, err = o.relations(); err != nil {
		return types.KnowledgeNeighborhoodOutput{}, err
	}
	dir, err := o.str("direction")
	if err != nil {
		return types.KnowledgeNeighborhoodOutput{}, err
	}
	switch no.Direction = types.EdgeDirection(dir); no.Direction {
	case "", types.EdgeIn, types.EdgeOut:
	default:
		return types.KnowledgeNeighborhoodOutput{}, fmt.Errorf("magus\\neighborhood: direction must be %q, %q or empty for both, got %q", types.EdgeOut, types.EdgeIn, dir)
	}
	if no.Collapse, err = o.strs("collapse"); err != nil {
		return types.KnowledgeNeighborhoodOutput{}, err
	}
	g, err := graphsFromContext(ctx, "neighborhood")
	if err != nil {
		return types.KnowledgeNeighborhoodOutput{}, err
	}
	kg, err := g.KnowledgeGraphWithSymbols(ctx)
	if err != nil {
		return types.KnowledgeNeighborhoodOutput{}, err
	}
	out, ok := kg.FocusNeighborhood(focus, no)
	if !ok {
		return types.KnowledgeNeighborhoodOutput{}, fmt.Errorf("magus\\neighborhood: no node matches %q", focus)
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

// optionReader decodes one member's options object. An unknown key or a value of the
// wrong shape raises: dropping `{dpeth = 2}` would answer a different question with
// nothing to say the typo did not take.
type optionReader struct {
	member string
	opts   map[string]any
}

func (o optionReader) only(keys ...string) error {
	for _, k := range slices.Sorted(maps.Keys(o.opts)) {
		if !slices.Contains(keys, k) {
			return fmt.Errorf("magus\\%s: unknown option %q (want %s)", o.member, k, strings.Join(keys, ", "))
		}
	}
	return nil
}

func (o optionReader) str(key string) (string, error) {
	v, ok := o.opts[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("magus\\%s: %s must be a string, got %T", o.member, key, v)
	}
	return s, nil
}

func (o optionReader) strs(key string) ([]string, error) {
	switch v := o.opts[key].(type) {
	case nil:
		return nil, nil
	case []string:
		return v, nil
	case []any:
		out := make([]string, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("magus\\%s: %s[%d] must be a string, got %T", o.member, key, i, item)
			}
			out[i] = s
		}
		return out, nil
	default:
		return nil, fmt.Errorf("magus\\%s: %s must be a list of strings, got %T", o.member, key, v)
	}
}

// count reads a non-negative integer option; absent is 0.
func (o optionReader) count(key string) (int, error) {
	v, ok := o.opts[key]
	if !ok {
		return 0, nil
	}
	n, err := intOptions(o.member, map[string]any{key: v}, key)
	return n[key], err
}

// relations reads the relations option, refusing a name the graph does not define: a
// misspelt relation filters every edge out and reads as an empty neighborhood.
func (o optionReader) relations() ([]types.RelationID, error) {
	names, err := o.strs("relations")
	if err != nil {
		return nil, err
	}
	rels := make([]types.RelationID, 0, len(names))
	for _, name := range names {
		if _, ok := types.KnowledgeRelation(types.RelationID(name)); !ok {
			var known []string
			for _, d := range types.KnowledgeRelationDefinitions() {
				known = append(known, string(d.ID))
			}
			return nil, fmt.Errorf("magus\\%s: unknown relation %q (want one of %s)", o.member, name, strings.Join(known, ", "))
		}
		rels = append(rels, types.RelationID(name))
	}
	return rels, nil
}

// MagusOutput backs magus\output: one target run's captured output by its ref, the bytes
// `magus query output <ref>` prints, with the run's identity beside them. It reads this
// checkout's output store, so a ref minted in another worktree does not resolve here.
func MagusOutput(ctx context.Context, ref string) (types.OutputRecord, error) {
	if !cache.LooksLikeRef(ref) {
		return types.OutputRecord{}, fmt.Errorf("magus\\output: %q is not an output ref (expected out<hex>, such as out1a2b3c)", ref)
	}
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.OutputRecord{}, errNoWorkspace("output")
	}
	cd, ok := ws.(workspaceCacheDir)
	if !ok {
		return types.OutputRecord{}, errors.New("magus\\output: this workspace has no cache directory")
	}
	data, desc, err := cache.NewOutputStore(cd.CacheDir()).ByRef(ref)
	if errors.Is(err, fs.ErrNotExist) {
		return types.OutputRecord{}, fmt.Errorf("magus\\output: no stored output for ref %q in this checkout", ref)
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

// intOptions decodes the integer options a graph member takes. An unknown key or a
// negative value is an error: dropping `{limt = 5}` would answer a different question
// with nothing to say the typo did not take.
func intOptions(member string, opts map[string]any, keys ...string) (map[string]int, error) {
	out := make(map[string]int, len(keys))
	for k, v := range opts {
		if !slices.Contains(keys, k) {
			return nil, fmt.Errorf("magus\\%s: unknown option %q (want %s)", member, k, strings.Join(keys, ", "))
		}
		// A Buzz integer arrives as int64 and a Buzz float as float64; int is what a Go
		// caller passes.
		var n int
		switch x := v.(type) {
		case int64:
			n = int(x)
		case float64:
			n = int(x)
		case int:
			n = x
		default:
			return nil, fmt.Errorf("magus\\%s: %s must be a number, got %T", member, k, v)
		}
		if n < 0 {
			return nil, fmt.Errorf("magus\\%s: %s must not be negative, got %d", member, k, n)
		}
		out[k] = n
	}
	return out, nil
}
