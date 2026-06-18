package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/hostbuzz"
	"github.com/egladman/magus/internal/interp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMagusModulesSharesDescribeCore is the parity lock for the native query
// methods: magus.modules() (host) and `magus describe modules` (CLI) are two thin
// adapters over the one typed core, hostbuzz.ModulesOutput. This asserts the
// records the host method marshals are exactly that core — same names, same docs,
// same per-method Buzz signatures — so the two surfaces can't drift.
func TestMagusModulesSharesDescribeCore(t *testing.T) {
	core := hostbuzz.ModulesOutput("") // what `magus describe modules` formats
	require.NotEmpty(t, core.Modules)

	// What a magusfile sees from magus.modules(): the same core, marshalled.
	got, ok := hostbuzz.ValueToAny(hostbuzz.RecordsVal(core.Modules)).([]any)
	require.True(t, ok)
	require.Len(t, got, len(core.Modules))
	for i, m := range core.Modules {
		rec := got[i].(map[string]any)
		assert.Equal(t, m.Name, rec["name"])
		assert.Equal(t, m.Doc, rec["doc"])
	}

	// Detail mode (magus.module) shares the same core, with typed methods + signatures.
	fs := hostbuzz.ModulesOutput("fs")
	require.Len(t, fs.Modules, 1)
	require.NotEmpty(t, fs.Modules[0].Methods)
	assert.NotEmpty(t, fs.Modules[0].Methods[0].Buzz, "each method carries its Buzz signature")
}

// TestMagusModulesEndToEnd drives the host methods from a real magusfile, proving
// they're wired onto the magus namespace and return usable typed records.
func TestMagusModulesEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.buzz", `import "magus";
export fun check(args: [str]) > void {
    final mods = magus.modules();
    if (mods.len() == 0) { magus.fatal("magus.modules() returned nothing"); }

    final fs = magus.module("fs");
    if (fs.name != "fs") { magus.fatal("magus.module(\"fs\").name was not fs"); }
    if (fs.methods.len() == 0) { magus.fatal("fs module has no methods"); }
    if (fs.methods[0].buzz == "") { magus.fatal("fs method missing its Buzz signature"); }
}`)
	srcs, err := interp.FindAll(dir)
	require.NoError(t, err)
	require.NoError(t, interp.Run(context.Background(), srcs[0], "check", nil, dir), "magus.modules/module end-to-end")
}
