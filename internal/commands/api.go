package commands

//go:generate go run ../../cmd/magus-utils api -out testdata/api.lock

import (
	_ "embed"
	"io/fs"
	"path"
	"slices"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/libs/clikit/generate"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// storeFormats are the shared store shapes this build reads.
// Another checkout's binary may have written the rows.
var storeFormats = []types.APIVersion{
	types.JobAPIVersion, types.JobResultAPIVersion,
	types.EventAPIVersion, types.StoredOutputAPIVersion,
}

// API is the contract testdata/api.lock holds, as sorted lines.
// It joins generate.Lock's command lines, "config <key>" per config key,
// "guard-rule <name>" per compiled guard rule, "store <api_version>" per
// store format and "spell <name>" per shipped spell. configKeys comes from
// config.KnownKeys, which this package does not import.
func API(configKeys []string) []string {
	out := generate.Lock(Root)
	for _, k := range configKeys {
		out = append(out, "config "+k)
	}
	for name := range builtin.Defaults() {
		out = append(out, "guard-rule "+name)
	}
	for _, f := range storeFormats {
		out = append(out, "store "+f.String())
	}
	out = append(out, shippedSpells()...)
	slices.Sort(out)
	return out
}

// shippedSpells is "spell <name>" per spell this build carries, named by
// its directory. A tree that adds or drops one changes the contract.
func shippedSpells() []string {
	var out []string
	err := fs.WalkDir(spells.Shipped(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && path.Base(p) == "spell.buzz" {
			out = append(out, "spell "+path.Dir(p))
		}
		return nil
	})
	if err != nil {
		panic("commands: the shipped spells do not list: " + err.Error())
	}
	return out
}

//go:embed testdata/api.lock
var lock []byte

// Lock is testdata/api.lock as this binary was built with it.
// It can differ from API: the drift test keeps the two equal, not the build.
func Lock() []byte { return slices.Clone(lock) }
