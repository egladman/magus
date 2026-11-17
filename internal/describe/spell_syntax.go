package describe

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// SpellSyntaxSummary folds the spell's declared language record into one line:
// which extensions, which comment openers, and what a stub may replace. The
// full record is in -o json; this line says only whether each part exists.
func SpellSyntaxSummary(t types.Spell) string {
	var parts []string
	if len(t.Extensions) > 0 {
		parts = append(parts, strings.Join(t.Extensions, " "))
	}
	if t.Syntax == nil {
		return strings.Join(parts, "; ")
	}
	if c := t.Syntax.Comments; c != nil {
		openers := slices.Clone(c.LineComments)
		for _, b := range c.BlockComments {
			openers = append(openers, fmt.Sprintf("%s %s", b.Open, b.Close))
		}
		parts = append(parts, "comments "+strings.Join(openers, ", "))
	}
	if st := t.Syntax.Stubs; st != nil {
		parts = append(parts, fmt.Sprintf("stubs %s (%s)", strings.Join(st.Kinds, ", "), st.BodyStyle))
	}
	return strings.Join(parts, "; ")
}
