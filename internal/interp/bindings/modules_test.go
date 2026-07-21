package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/host"
	"github.com/egladman/magus/internal/interp"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/std"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSupersetModules verifies that registerMagusModules exposes the magus host
// methods under the same bare names as Buzz's stdlib (a superset), and that the
// old magus/extra aggregate is gone.
func TestSupersetModules(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	registerMagusModules(ctx, sess)

	hasKey := func(t *testing.T, module, key string) {
		t.Helper()
		mod, ok := sess.SyntheticModule(module)
		require.True(t, ok, "module %q not registered", module)
		_, ok = mod.MapGet(key)
		assert.True(t, ok, "module %q missing key %q", module, key)
	}

	// os: Buzz stdlib (env, execute) and magus (exec, which) coexist on one module.
	hasKey(t, "os", "env")     // Buzz stdlib
	hasKey(t, "os", "execute") // Buzz stdlib
	hasKey(t, "os", "exec")    // magus
	hasKey(t, "os", "which")   // magus

	// fs: Buzz stdlib (makeDirectory) plus magus (glob, readFile).
	hasKey(t, "fs", "makeDirectory") // Buzz stdlib
	hasKey(t, "fs", "glob")          // magus
	hasKey(t, "fs", "readFile")      // magus

	// crypto: Buzz stdlib (hash) plus magus digests and the byte-level companions.
	hasKey(t, "crypto", "hash")       // Buzz stdlib
	hasKey(t, "crypto", "sha256Hex")  // magus
	hasKey(t, "crypto", "hmacSha256") // magus/extra companion merged in

	// Modules Buzz's stdlib lacks become new bare imports.
	hasKey(t, "http", "get")         // magus
	hasKey(t, "http", "download")    // magus/extra http companion merged in
	hasKey(t, "vcs", "shortHash")    // magus
	hasKey(t, "archive", "compress") // magus
	hasKey(t, "time", "format")      // magus
	hasKey(t, "markdown", "toHtml")  // magus

	// The aggregate import and its byte-level siblings are gone.
	for _, gone := range []string{"magus/extra", "magus/extra/http", "magus/extra/crypto"} {
		_, ok := sess.SyntheticModule(gone)
		assert.False(t, ok, "module %q should no longer be registered", gone)
	}
}

// TestEveryHostModuleIsWired guards against a std host module being declared (and
// documented) but never exposed to Buzz sessions - the gap that left template,
// toml, and uuid unreachable after they were added to std/ with host/gen
// trampolines but omitted from magusModules. Every module std.All() reports, save
// the hand-assembled "magus" namespace, must resolve as a synthetic module with
// its first declared method present.
func TestEveryHostModuleIsWired(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	registerMagusModules(ctx, sess)

	for _, m := range std.All() {
		// "magus" is not a bare import; it is wired as the magus.* namespace in
		// buzz.go, not through magusModules.
		if m.Name == "magus" {
			continue
		}
		mod, ok := sess.SyntheticModule(m.Name)
		if !assert.Truef(t, ok, "host module %q is declared in std but not wired into a Buzz session; add it to magusModules", m.Name) {
			continue
		}
		if len(m.Methods) == 0 {
			continue
		}
		key := host.CamelCase(m.Methods[0].Name)
		if bn := m.Methods[0].BuzzName; bn != "" {
			key = bn
		}
		_, ok = mod.MapGet(key)
		assert.Truef(t, ok, "host module %q is registered but missing method %q", m.Name, key)
	}
}

// TestMagusModulesSharesDescribeCore is the parity lock for the native query
// methods: magus.modules() (host) and `magus describe modules` (CLI) are two thin
// adapters over the one typed core, host.ModulesOutput. This asserts the records
// the host method marshals are exactly that core (same names, docs, per-method Buzz
// signatures) so the two surfaces can't drift.
func TestMagusModulesSharesDescribeCore(t *testing.T) {
	core := host.ModulesOutput("") // what `magus describe modules` formats
	require.NotEmpty(t, core.Modules)

	// What a magusfile sees from magus.modules(): the same core, marshalled.
	got, ok := host.ValueToAny(host.MapsVal(core.Modules)).([]any)
	require.True(t, ok)
	require.Len(t, got, len(core.Modules))
	for i, m := range core.Modules {
		rec := got[i].(map[string]any)
		assert.Equal(t, m.Name, rec["name"])
		assert.Equal(t, m.Doc, rec["doc"])
	}

	// Detail mode (magus.module) shares the same core, with typed methods + signatures.
	fs := host.ModulesOutput("fs")
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

// TestTemplatePartialsEndToEnd drives the template module from a real magusfile,
// proving import "template" resolves and renderPartials expands {{>name}} includes,
// interpolates the shared context, and HTML-escapes {{var}} values. Backtick raw
// strings carry the Mustache verbatim (a double-quoted "{{x}}" would collide with
// Buzz's own {expr} interpolation).
func TestTemplatePartialsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "magusfile.buzz", "import \"magus\";\n"+
		"import \"template\";\n"+
		"export fun check(args: [str]) > void {\n"+
		"    final page = `{{>header}}[{{body}}]{{>footer}}`;\n"+
		"    final partials = {\"header\": `<h>{{title}}</h>`, \"footer\": `<f/>`};\n"+
		"    final got = template.renderPartials(page, {\"title\": \"magus\", \"body\": \"hi & <b>\"}, partials);\n"+
		"    final want = \"<h>magus</h>[hi &amp; &lt;b&gt;]<f/>\";\n"+
		"    if (got != want) { magus.fatal(\"renderPartials mismatch: got \" + got); }\n"+
		"}")
	srcs, err := interp.FindAll(dir)
	require.NoError(t, err)
	require.NoError(t, interp.Run(context.Background(), srcs[0], "check", nil, dir), "template.renderPartials end-to-end")
}
