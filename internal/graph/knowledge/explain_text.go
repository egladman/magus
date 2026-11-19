package knowledge

import (
	"fmt"

	"github.com/egladman/magus/types"
)

// ResolutionNote is the line explain prints above a card it did not reach by node ID, ""
// for an ID match.
func ResolutionNote(asked string, out types.KnowledgeExplainOutput) string {
	switch out.Resolution {
	case types.ResolvedPath:
		return fmt.Sprintf("%q resolved by path to %s\n", asked, out.Node.ID)
	case types.ResolvedFuzzy:
		return fmt.Sprintf("%q matched no node exactly; showing the closest, %s (explain it by ID to be sure)\n", asked, out.Node.ID)
	case types.ResolvedID:
	}
	return ""
}
