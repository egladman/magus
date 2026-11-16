package knowledge

import (
	"fmt"
	"io"
	"strings"

	"github.com/egladman/magus/libs/termkit/layout"
	"github.com/egladman/magus/types"
)

// StatsText prints the knowledge graph's shape as plain text.
func StatsText(w io.Writer, out types.KnowledgeStats) error {
	fmt.Fprintf(w, "definition: %s\n\n", out.Definition)
	fmt.Fprintf(w, "graph: %d nodes, %d edges\n", out.NodeCount, out.EdgeCount)
	// The connectivity lens: fragmentation is a data-quality signal. One component with few isolated nodes
	// is a well-linked graph; many components / a high isolated count means the builder left relationships
	// undefined (the discoverability gap to chase).
	fmt.Fprintf(w, "connectivity: %d component(s), largest holds %d, %d isolated node(s)\n\n",
		out.ComponentCount, out.LargestComponentSize, out.IsolatedCount)
	fmt.Fprintln(w, "god nodes (most connected):")
	fmt.Fprintf(w, "  %6s  %4s  %4s  %-11s  %s\n", "DEGREE", "IN", "OUT", "KIND", "LABEL")
	for _, g := range out.Gods {
		fmt.Fprintf(w, "  %6d  %4d  %4d  %-11s  %s\n", g.Degree, g.In, g.Out, g.Kind, g.Label)
	}
	if len(out.Orphans) > 0 {
		// Orphans is a capped sample; name the true isolated total so a large graph does not read as if
		// only len(Orphans) nodes are unlinked.
		if out.IsolatedCount > len(out.Orphans) {
			fmt.Fprintf(w, "\norphans (showing %d of %d isolated):\n", len(out.Orphans), out.IsolatedCount)
		} else {
			fmt.Fprintf(w, "\norphans (%d):\n", len(out.Orphans))
		}
		for _, o := range out.Orphans {
			fmt.Fprintf(w, "  %-11s  %-26s  %s\n", o.Kind, layout.ClipBytes(o.Label, 26), o.Reason)
		}
	}
	if len(out.Coverage) > 0 {
		fmt.Fprintln(w, "\ndoc coverage:")
		for _, c := range out.Coverage {
			fmt.Fprintf(w, "  %-11s  %d/%d (%d%%)", c.Kind, c.Documented, c.Total, c.Percent)
			if len(c.Undocumented) > 0 {
				fmt.Fprintf(w, "  missing: %s", strings.Join(c.Undocumented, ", "))
			}
			fmt.Fprintln(w)
		}
	}
	return nil
}
