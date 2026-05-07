package bindings_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/egladman/magus/internal/interp/bindings"
	_ "github.com/egladman/magus/internal/interp/engine/lua/gopherlua"
	_ "github.com/egladman/magus/internal/interp/engine/lua/luajit"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// TestTealFunctionOpDispatch verifies a Teal spell can author an in-VM function-op
// (a function-valued mgs_listTargets entry) that magus dispatches at invoke time,
// receiving its inputs through the cb callback — the capability that previously
// required Buzz. The op writes a marker file from the params it receives via cb,
// proving both in-VM execution and req.Params delivery. It also returns a bool the
// host marshals back as Data, the contract a remote cache backend's get_artifact uses.
func TestTealFunctionOpDispatch(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/fn.tl", `
local fs = require("magus.extra.fs")
return {
    mgs_getName = function(): string return "tealfn" end,
    mgs_listTargets = function(): any
        return {
            ["write-marker"] = function(_t: any, cb: function(any)): boolean
                local io: {string:any} = {}
                cb(io)
                fs.write_file(io.dest as string, "ok:" .. (io.hash as string))
                return true
            end,
        }
    end,
}
`)
	writeFile(t, dir, "magusfile.tl", `
local fn = magus.spell.load("spells/fn.tl")
magus.project.register(".", { spells = {fn} })
`)
	if err := parseMagusfile(t, dir); err != nil {
		t.Fatalf("parse: %v", err)
	}

	// A function-op spell registers eagerly at load (capturing its source) so the
	// in-VM invoker can re-dispatch it; a fork-only spell would defer to bind.
	sp, ok := project.DefaultSpellRegistry().Lookup("tealfn")
	if !ok {
		t.Fatal("tealfn not registered (function-op spell should register eagerly)")
	}

	dest := filepath.Join(dir, "marker.txt")
	resp, err := sp.Invoke(context.Background(), types.InvokeRequest{
		Target: "write-marker",
		Params: map[string]any{"dest": dest, "hash": "abc"},
	})
	if err != nil {
		t.Fatalf("invoke function-op: %v", err)
	}
	if hit, _ := resp.Data.(bool); !hit {
		t.Errorf("Data = %v, want true (handler return marshalled back)", resp.Data)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read marker (op did not run in-VM?): %v", err)
	}
	if string(got) != "ok:abc" {
		t.Errorf("marker = %q, want %q (req.Params not delivered via cb?)", got, "ok:abc")
	}
}

// TestTealSpellLoadFileAsBackend verifies a Teal function-op spell is accepted as a
// remote cache backend selector (loadSpellFile previously rejected non-.bzz paths),
// proving the .tl path registers a function-op driver the cache can drive.
func TestTealSpellLoadFileAsBackend(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "spells/cache.tl", `
return {
    mgs_getName = function(): string return "tealbackend" end,
    mgs_listTargets = function(): any
        return {
            enabled = function(_t: any, _cb: function(any)): boolean return true end,
        }
    end,
}
`)
	writeFile(t, dir, "magusfile.tl", `
local backend = magus.spell.load("spells/cache.tl")
magus.project.register(".", { spells = {backend} })
magus.cache.remote(backend)
`)
	if err := parseMagusfile(t, dir); err != nil {
		t.Fatalf("parse with Teal cache backend: %v", err)
	}
	if _, ok := project.DefaultSpellRegistry().Lookup("tealbackend"); !ok {
		t.Fatal("tealbackend not registered")
	}
}
