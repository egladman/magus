package manpage_test

import (
	"flag"
	"fmt"

	"github.com/egladman/magus/internal/manpage"
)

// ExampleSegment shows how to build a Segment descriptor for a CLI subcommand.
// Segments are consumed by the man-page generator and the registry builder to
// produce magus(1) and its per-subcommand man pages.
func ExampleSegment() {
	seg := manpage.Segment{
		Name:  "run",
		Short: "run a target for selected projects",
		Usage: "magus run <target> [flags] [project...]",
		BuildFlags: func(fs *flag.FlagSet) {
			fs.Bool("dry-run", false, "print what would run without executing")
		},
		Examples: []manpage.Example{
			{Comment: "Build all projects", Command: "magus run build"},
			{Comment: "Test a single project", Command: "magus run test myapp"},
		},
	}

	fmt.Println(seg.Name)
	fmt.Println(seg.Short)
	fmt.Println(len(seg.Examples))
	// Output:
	// run
	// run a target for selected projects
	// 2
}
