//go:build cgo_engines

// LuaJIT 2.1 bindings for the comparison harness, via cgo. cgo is not permitted
// in _test.go files, so the low-level primitives live here (a regular,
// build-tagged .go file) and the benchmark loop that uses them lives in
// engines_cgo_test.go. Compiled only under -tags cgo_engines (needs
// CGO_ENABLED=1 + libluajit-5.1-dev).
package comparison

/*
#cgo pkg-config: luajit
#include <stdlib.h>
#include <lua.h>
#include <lauxlib.h>
#include <lualib.h>

// Wrappers for Lua 5.1 / LuaJIT macros that cgo cannot call directly.
static int  lj_load(lua_State *L, const char *s) { return luaL_loadstring(L, s); }
static int  lj_ref(lua_State *L)                 { return luaL_ref(L, LUA_REGISTRYINDEX); }
static void lj_geti(lua_State *L, int ref)       { lua_rawgeti(L, LUA_REGISTRYINDEX, ref); }
static int  lj_pcall(lua_State *L)               { return lua_pcall(L, 0, 0, 0); }
static const char *lj_err(lua_State *L)          { return lua_tostring(L, -1); }
static void lj_settop0(lua_State *L)             { lua_settop(L, 0); }
*/
import "C"

import "unsafe"

// luaState wraps a LuaJIT interpreter. The C pointer never crosses into a
// _test.go file; only these methods do.
type luaState struct{ L *C.lua_State }

func luaNew() luaState {
	L := C.luaL_newstate()
	C.luaL_openlibs(L) // math (sqrt), string, table, …
	return luaState{L}
}

func (s luaState) close() { C.lua_close(s.L) }

// loadRef compiles src and stores the chunk in the registry, returning a ref to
// re-push it cheaply each warm iteration. A non-empty string is a load error.
func (s luaState) loadRef(src string) (int, string) {
	c := C.CString(src)
	defer C.free(unsafe.Pointer(c))
	if C.lj_load(s.L, c) != 0 {
		return 0, C.GoString(C.lj_err(s.L))
	}
	return int(C.lj_ref(s.L)), ""
}

// callRef re-pushes the stored chunk and runs it. Non-empty string is an error.
func (s luaState) callRef(ref int) string {
	C.lj_geti(s.L, C.int(ref))
	if C.lj_pcall(s.L) != 0 {
		return C.GoString(C.lj_err(s.L))
	}
	C.lj_settop0(s.L)
	return ""
}

// loadCall compiles and runs src once (the fresh path). Non-empty string is an error.
func (s luaState) loadCall(src string) string {
	c := C.CString(src)
	defer C.free(unsafe.Pointer(c))
	if C.lj_load(s.L, c) != 0 {
		return C.GoString(C.lj_err(s.L))
	}
	if C.lj_pcall(s.L) != 0 {
		return C.GoString(C.lj_err(s.L))
	}
	return ""
}
