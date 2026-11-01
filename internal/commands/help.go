package commands

import (
	"fmt"
	"io"

	"github.com/egladman/magus/libs/cmdline"
)

// WriteChildHelp writes one subcommand's usage, long description and examples
// from the registry, so -h, the man page and the docs page read the same words.
func WriteChildHelp(w io.Writer, parent, child string) {
	var parentCmd *cmdline.Command
	for i := range Root.Children {
		if Root.Children[i].Name == parent {
			parentCmd = &Root.Children[i]
			break
		}
	}
	if parentCmd == nil {
		return
	}
	for _, c := range parentCmd.Children {
		if c.Name != child {
			continue
		}
		fmt.Fprintf(w, "Usage: %s\n\n%s\n\n", c.Usage, c.Long)
		if len(c.Examples) == 0 {
			return
		}
		fmt.Fprintln(w, "Example:")
		for _, ex := range c.Examples {
			fmt.Fprintf(w, "  %s\n", ex.Command)
		}
		return
	}
}
