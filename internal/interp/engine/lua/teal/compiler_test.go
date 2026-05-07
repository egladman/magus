package teal_test

import (
	"context"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp/engine"
	lua "github.com/egladman/magus/internal/interp/engine/lua"
	"github.com/egladman/magus/internal/interp/engine/lua/teal"

	_ "github.com/egladman/magus/internal/interp/engine/lua/gopherlua"
)

func newSession(t *testing.T) lua.Session {
	t.Helper()
	eng := engine.Lookup("gopherlua")
	if eng == nil {
		t.Skip("gopherlua backend not registered")
	}
	sess, err := eng.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	r, ok := sess.(lua.Session)
	if !ok {
		t.Fatal("gopherlua session is not a lua.Session")
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// eval runs src (which must `return` a single value) and returns the result.
func eval(t *testing.T, r lua.Session, src string) engine.Value {
	t.Helper()
	fn, err := r.LoadString(src)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	if err := r.Call(engine.CallParams{Fn: fn, NRet: 1, Protect: true}); err != nil {
		t.Fatalf("run %q: %v", src, err)
	}
	v := r.Get(-1)
	r.Pop(1)
	return v
}

// TestLoadShimAcceptsString is the regression test for the gopher-lua load()
// defect: tl.lua's runtime package loader calls load(<string>), but unshimmed
// gopher-lua load only accepts a reader function. After InstallUtf8Shim,
// load(<string>) must compile and return a callable chunk.
func TestLoadShimAcceptsString(t *testing.T) {
	r := newSession(t)
	teal.InstallUtf8Shim(r)

	got := eval(t, r, `local f = load("return 41 + 1"); return f()`)
	if n, ok := got.AsNumber(); !ok || n != 42 {
		t.Fatalf("load(string) chunk returned %v (ok=%v); want 42", got.String(), n)
	}
}

// TestLoadShimStringSyntaxError verifies a malformed string chunk follows Lua's
// load contract: return (nil, message) rather than raising.
func TestLoadShimStringSyntaxError(t *testing.T) {
	r := newSession(t)
	teal.InstallUtf8Shim(r)

	got := eval(t, r, `local f, err = load("this is not lua ="); return f == nil and err ~= nil`)
	if !got.AsBool() {
		t.Fatal("load(bad string) should return (nil, message)")
	}
}

// TestLoadShimPreservesReaderForm verifies the Lua 5.1 reader-function form of
// load still works after the shim — it delegates to the original load.
func TestLoadShimPreservesReaderForm(t *testing.T) {
	r := newSession(t)
	teal.InstallUtf8Shim(r)

	const src = `
local parts = {"return 6 ", "* 7"}
local i = 0
local f = load(function() i = i + 1; return parts[i] end)
return f()`
	got := eval(t, r, src)
	if n, ok := got.AsNumber(); !ok || n != 42 {
		t.Fatalf("load(reader) chunk returned %v (ok=%v); want 42", got.String(), n)
	}
}

// TestLoadShimPreservesChunkName verifies the chunkname passed as load's second
// argument (e.g. tl.lua's "@"..filename) reaches the loaded chunk, so runtime
// errors raised inside it are labelled with the source file rather than "<string>".
func TestLoadShimPreservesChunkName(t *testing.T) {
	r := newSession(t)
	teal.InstallUtf8Shim(r)

	// The chunk raises at runtime; pcall captures the message, which Lua prefixes
	// with "<chunkname>:<line>:".
	const src = `
local f = assert(load("local x = nil + 1", "@spells/locreq.tl"))
local ok, err = pcall(f)
return err`
	got := eval(t, r, src)
	msg, ok := got.AsString()
	if !ok {
		t.Fatalf("pcall error is not a string: %v", got.String())
	}
	if !strings.Contains(msg, "spells/locreq.tl") {
		t.Fatalf("error %q does not carry the chunkname; chunkname was dropped", msg)
	}
}

// TestLoadShimArity verifies the shim preserves load's native return arity:
// one value on success (not padded with a trailing nil).
func TestLoadShimArity(t *testing.T) {
	r := newSession(t)
	teal.InstallUtf8Shim(r)

	if got := eval(t, r, `return select("#", load("return 1"))`); func() bool {
		n, ok := got.AsNumber()
		return !ok || n != 1
	}() {
		t.Fatalf("load(string) success arity = %v; want 1", got.String())
	}
	if got := eval(t, r, `return select("#", load(function() return nil end))`); func() bool {
		n, ok := got.AsNumber()
		return !ok || n != 1
	}() {
		t.Fatalf("load(reader) success arity = %v; want 1", got.String())
	}
}
