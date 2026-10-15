package commands

//go:generate go run ../../cmd/magus-utils api -out testdata/api.lock

import (
	"slices"

	"github.com/egladman/magus/libs/cmdline/generate"
)

// API is magus's public command-line API as the sorted lines testdata/api.lock holds:
// the tree's lines from generate.Lock plus "config <key>" for each config key.
// configKeys comes from config.KnownKeys, which this package does not import.
func API(configKeys []string) []string {
	out := generate.Lock(Root)
	for _, k := range configKeys {
		out = append(out, "config "+k)
	}
	slices.Sort(out)
	return out
}
