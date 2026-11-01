package knowledge

import (
	"cmp"
	"fmt"
	"io"
	"slices"

	"github.com/egladman/magus/types"
)

// WriteSummary writes the text form of a graph export: a routing summary with
// counts by kind and relation, not a data dump.
func WriteSummary(w io.Writer, out types.KnowledgeGraphOutput) {
	fmt.Fprintf(w, "definition: %s\n\n", out.Definition)
	fmt.Fprintf(w, "knowledge graph (schema v%d): %d nodes, %d edges\n\n", out.SchemaVersion, out.NodeCount, out.EdgeCount)
	fmt.Fprintln(w, "nodes by kind:")
	for _, kv := range countBy(len(out.Nodes), func(i int) string { return out.Nodes[i].Kind }) {
		fmt.Fprintf(w, "  %-11s %d\n", kv.key, kv.n)
	}
	fmt.Fprintln(w, "\nedges by relation:")
	for _, kv := range countBy(len(out.Links), func(i int) string { return string(out.Links[i].Relation) }) {
		fmt.Fprintf(w, "  %-11s %d\n", kv.key, kv.n)
	}
	fmt.Fprintln(w, "\nRun with -o json (node-link) or -o graphml for the full graph.")
}

type keyCount struct {
	key string
	n   int
}

// countBy tallies n items by the key each index maps to, sorted by key for
// stable output.
func countBy(n int, keyOf func(i int) string) []keyCount {
	m := map[string]int{}
	for i := range n {
		m[keyOf(i)]++
	}
	out := make([]keyCount, 0, len(m))
	for k, v := range m {
		out = append(out, keyCount{k, v})
	}
	slices.SortFunc(out, func(a, b keyCount) int { return cmp.Compare(a.key, b.key) })
	return out
}
