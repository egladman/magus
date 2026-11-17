package hint

import (
	"fmt"

	"github.com/egladman/magus/types"
)

// CanonicalSpelling is the nudge toward the canonical name when the user typed another
// one, one message per spelling. The fact comes off the parsed Target
// (Declared/DeclaredCharms), so this is presentation only: types.ParseTarget stays a pure
// function and the server and MCP paths get the same information without inheriting
// stderr output. It is empty for canonical input.
func CanonicalSpelling(t types.Target) []string {
	var msgs []string
	if t.Declared != "" {
		msgs = append(msgs, fmt.Sprintf("target %q is canonically %q; both work, %q is what magus reports", t.Declared, t.Name, t.Name))
	}
	for _, c := range t.DeclaredCharms {
		msgs = append(msgs, fmt.Sprintf("charm %q is canonically %q", c, types.NormalizeCharm(c)))
	}
	return msgs
}
