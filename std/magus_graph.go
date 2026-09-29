//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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
		return types.KnowledgeQueryOutput{}, errors.New("magus\\query: needs search terms, e.g. \"kind=spell go\"")
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
	kg, err := g.KnowledgeGraph(ctx, false)
	if err != nil {
		return types.KnowledgeExplainOutput{}, err
	}
	out, ok := kg.Explain(node)
	if !ok {
		// explain reads the symbol-free graph, so a miss on a name that could be a code
		// symbol is a blind spot, not an absence.
		if knowledge.Answer(node, false, graphCoverage(ctx, g, node, false)).Verdict == types.VerdictUnknown {
			return types.KnowledgeExplainOutput{}, fmt.Errorf("magus\\explain: no node matches %q in the domain graph; code symbols are not loaded here, so ask magus\\refs", node)
		}
		return types.KnowledgeExplainOutput{}, fmt.Errorf("magus\\explain: no node matches %q", node)
	}
	return out, nil
}

// MagusPath backs magus\path: the shortest chain of edges between node and to, the
// record `magus path -o json` prints. A resolved pair with no connection is an answer
// (found is false); only an endpoint that resolves to nothing raises.
func MagusPath(ctx context.Context, node, to string) (types.KnowledgePathOutput, error) {
	if node == "" || to == "" {
		return types.KnowledgePathOutput{}, errors.New("magus\\path: needs both endpoints")
	}
	g, err := graphsFromContext(ctx, "path")
	if err != nil {
		return types.KnowledgePathOutput{}, err
	}
	kg, err := g.KnowledgeGraph(ctx, false)
	if err != nil {
		return types.KnowledgePathOutput{}, err
	}
	out, ok := kg.Path(node, to)
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

// MagusOutput backs magus\output: one target run's captured output by its ref, the bytes
// `magus query output <ref>` prints, with the run's identity beside them. It reads this
// checkout's output store, so a ref minted in another worktree does not resolve here.
func MagusOutput(ctx context.Context, ref string) (types.OutputRecord, error) {
	if !cache.LooksLikeRef(ref) {
		return types.OutputRecord{}, fmt.Errorf("magus\\output: %q is not an output ref (expected out<hex>, e.g. out1a2b3c)", ref)
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

// recordMap converts a record to the map a Buzz caller receives, keyed exactly as its
// `-o json` form so a script and the CLI read one shape. Whole numbers decode as int64:
// a JSON count decoded to float64 would reach Buzz as a double.
func recordMap(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.UnmarshalLossless(raw, &out); err != nil {
		return nil, err
	}
	// numbersToBuzz's map[string]any case always hands the same map back.
	converted, ok := numbersToBuzz(out).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("recordMap: numbersToBuzz returned %T, not a map", converted)
	}
	return converted, nil
}

// jsonNumber is encoding/json.Number's method set, named here because this tree reaches
// encoding/json only through internal/json.
type jsonNumber interface {
	Int64() (int64, error)
	Float64() (float64, error)
}

func numbersToBuzz(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = numbersToBuzz(e)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = numbersToBuzz(e)
		}
		return x
	case jsonNumber:
		if n, err := x.Int64(); err == nil {
			return n
		}
		f, _ := x.Float64()
		return f
	}
	return v
}
