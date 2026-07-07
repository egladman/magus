package manpage

//go:generate go run ../../cmd/magus-utils api -out testdata/api.lock

import (
	"flag"
	"sort"
)

// API returns magus's public CLI API as a sorted, flat list of stable tokens: one
// line per subcommand, per subcommand flag, per project target, and per config key.
// It is emitted to a committed .lock snapshot (the same plain sorted-line format as
// urls.lock) and drift-gated, so a diff shows exactly what changed in the public
// interface - a removed line is a removed, backward-incompatible element that a
// human reviews and records in the changelog.
//
// It reuses the same Command registry the man pages render from, so the snapshot and
// the man pages can never disagree. configKeys is passed in (from config.KnownKeys)
// rather than imported, keeping this package free of a config dependency.
func API(configKeys []string) []string {
	var out []string
	for _, c := range All {
		out = append(out, "subcommand "+c.Name)
		if c.BuildFlags != nil {
			fs := flag.NewFlagSet(c.Name, flag.ContinueOnError)
			c.BuildFlags(fs)
			fs.VisitAll(func(f *flag.Flag) {
				out = append(out, "flag "+c.Name+"/"+f.Name)
			})
		}
		for _, t := range c.Targets {
			out = append(out, "target "+c.Name+"/"+t.Name)
		}
	}
	for _, k := range configKeys {
		out = append(out, "config "+k)
	}
	sort.Strings(out)
	return out
}
