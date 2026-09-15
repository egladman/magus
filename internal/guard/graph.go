package guard

import "slices"

// The guard's half of the index-staleness fact. The load-bearing half rides the command's
// own output (cmd/magus/staleindex.go): it works on every host with nothing wired. This one
// reaches a host that runs a pre-tool hook, and it arrives one call EARLIER: before the
// stale answer is read rather than under it.
//
// It is an advisory and never a deny. A stale index still answers, the answer is still
// worth having, and blocking a read to enforce tidiness is the shape of guard rule this
// project has already reverted once.

// graphReadSubcommands are the verbs that answer FROM the index, so a stale one changes
// what they report.
var graphReadSubcommands = []string{"refs", "query", "explain", "path"}

// commandReadsGraph reports whether a shell line would run one of them.
//
// Through the parser rather than a pattern, for the reason commandRunsGate is: it resolves
// quoting and peels wrappers, so a launcher prefix reads the same as the bare form and the
// word `refs` inside a quoted argument reads as neither.
//
// `magus query output <ref>` is exempt. It reads a captured log, not the graph, and it is
// the same exemption the output-pipe rule carves out for the same reason.
func commandReadsGraph(command string) bool {
	cmds, ok := ParseCommands(command)
	if !ok {
		return false
	}
	for _, c := range cmds {
		if c.Name != "magus" || len(c.Args) == 0 {
			continue
		}
		if !slices.Contains(graphReadSubcommands, c.Args[0]) {
			continue
		}
		if c.Args[0] == "query" && len(c.Args) >= 2 && c.Args[1] == "output" {
			continue
		}
		return true
	}
	return false
}
