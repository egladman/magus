package main

import (
	"flag"
	"os"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/manpage"
)

// runAPI writes magus's public CLI API as a sorted .lock snapshot (the same plain
// sorted-line format as urls.lock): one line per subcommand, flag, project target,
// and config key. Committed and drift-gated, its diff is what a reviewer inspects
// for backward-incompatible changes - a removed line is a removed API element.
func runAPI(args []string) error {
	fs := flag.NewFlagSet("api", flag.ContinueOnError)
	out := fs.String("out", "", "write the public-API lock to this path (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	data := []byte(strings.Join(manpage.API(config.KnownKeys()), "\n") + "\n")
	if *out == "" {
		_, err := os.Stdout.Write(data)
		return err
	}
	return os.WriteFile(*out, data, 0o644)
}
