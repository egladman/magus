package generate

import (
	"flag"
	"sort"

	"github.com/egladman/magus/libs/cmdline"
)

// Lock lists root's public command-line API as sorted lines, one per top-level
// command, per flag of one, and per operand: "subcommand run", "flag run/no-cache",
// "operand run/build". Committed and diffed, a removed line is a removed element a
// reviewer can see. The caller appends lines for anything the tree does not hold,
// such as config keys, and sorts again.
func Lock(root cmdline.Command) []string {
	var out []string
	for _, c := range root.Children {
		out = append(out, "subcommand "+c.Name)
		visitFlags(c, func(f *flag.Flag) {
			out = append(out, "flag "+c.Name+"/"+f.Name)
		})
		for _, o := range c.Operands {
			out = append(out, "operand "+c.Name+"/"+o.Name)
		}
	}
	sort.Strings(out)
	return out
}
