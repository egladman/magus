package knowledge

import (
	"context"
	"fmt"
	"io"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// GraphRead is one read's parameters, as a client sends them to a server.
type GraphRead struct {
	Input  string `json:"input"`
	Budget int    `json:"budget,omitempty"`
	// Nearest asks query for the closest node id when nothing matches. Only the text view
	// prints one, and finding it is a fuzzy pass over the whole graph.
	Nearest bool `json:"nearest,omitempty"`
	// Occurrences asks refs for the occurrence read as well as the reference list.
	Occurrences bool `json:"occurrences,omitempty"`
	// Config is config.ReadDigest of the configuration the client reads under.
	Config string `json:"config"`
	// Env is config.EnvDigests of the client's environment. An index freshness verdict reads the
	// process environment (the cache key folds in allowed variables, and PATH picks the
	// binary a version probe runs), so the server compares the variables its verdict
	// depends on, without any value crossing the socket.
	Env map[string]string `json:"env"`
}

// QueryResult is what one `magus query` search found: everything its output formats
// render apart from the breadcrumbs, which depend on who is asking.
type QueryResult struct {
	Out     types.KnowledgeQueryOutput `json:"out"`
	Nearest string                     `json:"nearest,omitempty"`
}

// ExplainResult is what one `magus explain` lookup found.
type ExplainResult struct {
	Out   types.KnowledgeExplainOutput `json:"out"`
	Found bool                         `json:"found"`
	// Answer judges the lookup: on a miss, whether the node is absent or merely unseen; on
	// a hit, whether the index it came from is current. explain's record carries no answer
	// of its own, so this is the one every other client builds.
	Answer  types.KnowledgeAnswer `json:"answer"`
	Nearest string                `json:"nearest,omitempty"`
}

// WriteQueryText writes res as `magus query` prints it in text form, with printNext under
// it, and returns the error that sets the exit status. The nearest-node hint goes to
// stderr, not w.
func WriteQueryText(ctx context.Context, w io.Writer, printNext func(io.Writer), res QueryResult) error {
	out := res.Out
	fmt.Fprintf(w, "query: %s\n", out.Query)
	fmt.Fprintf(w, "matches: %d  (neighborhood budget %d)\n\n", out.MatchCount, out.Budget)
	if out.MatchCount == 0 {
		PrintVerdict(w, out.Answer, hint.Refs.With("<name>"))
		EmitNearest(ctx, res.Nearest)
		if err := ReportIndexStaleness(w, out.Answer); err != nil {
			return err
		}
		// An empty result set is still a legitimate answer to a search, so this exits 0 on
		// `absent` and only fails on `unknown`. See exitForQuery.
		return ExitForQuery(out)
	}
	shown := out.Matches
	if len(shown) > 20 {
		shown = shown[:20]
	}
	for _, m := range shown {
		fmt.Fprintf(w, "  %-7d %s  [%s]\n", m.Score, m.ID, m.Kind)
	}
	if len(out.Matches) > len(shown) {
		fmt.Fprintf(w, "  ... and %d more\n", len(out.Matches)-len(shown))
	}
	fmt.Fprintf(w, "\nneighborhood: %d nodes, %d edges\n", len(out.Nodes), len(out.Links))
	fmt.Fprintln(w, "Run with -o json for the full subgraph.")
	printNext(w)
	return ReportIndexStaleness(w, out.Answer)
}
