//go:build cgo

// Package luajit implements the engine.Engine interface using LuaJIT 2.1 via
// cgo. It registers itself at init() time:
//
//	import _ "github.com/egladman/magus/internal/interp/engine/lua/luajit"
package luajit

/*
#include <lua.h>
#include <lualib.h>
#include <lauxlib.h>
#include <luajit.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// Forward declarations for Go-exported trampolines.
extern int goFuncTrampoline(lua_State *L);
extern void hookTrampoline(lua_State *L, lua_Debug *ar);

// goFuncDispatch is the registered lua_CFunction for every Go closure. It calls
// the Go trampoline (which never longjmps — a Go RaiseError surfaces as a
// recovered panic), then raises the Lua error from this pure-C frame when the
// trampoline signals one with a negative return. Raising lua_error here rather
// than inside the Go frame is essential: lua_error longjmps back to the
// enclosing lua_pcall, and a longjmp must not cross a cgo-exported Go frame
// (doing so aborts with "unprotected error in call to Lua API"). On an error
// signal the trampoline has already pushed the message string onto L for
// lua_error to consume.
static int goFuncDispatch(lua_State *L) {
    int nret = goFuncTrampoline(L);
    if (nret < 0) {
        return lua_error(L); // never returns; longjmps to the nearest lua_pcall
    }
    return nret;
}

// Inline wrappers for Lua macros that cgo cannot call directly.

static void lj_pop(lua_State *L, int n)       { lua_pop(L, n); }
static void lj_setglobal(lua_State *L, const char *s) { lua_setglobal(L, s); }
static void lj_getglobal(lua_State *L, const char *s) { lua_getglobal(L, s); }
static int  lj_upvalueindex(int i)            { return lua_upvalueindex(i); }
static const char *lj_checkstring(lua_State *L, int n) { return luaL_checkstring(L, n); }
static void lj_pushcfunction(lua_State *L, lua_CFunction f) { lua_pushcfunction(L, f); }

// Store a Go closure handle as a Lua C closure with one upvalue (the handle
// as light userdata). goFuncTrampoline retrieves it via upvalue index 1.
static void pushGoFunc(lua_State *L, uintptr_t handle) {
    lua_pushlightuserdata(L, (void*)(uintptr_t)handle);
    lua_pushcclosure(L, goFuncDispatch, 1);
}

// Cancellation context handle stored in the Lua registry.
static const char *CANCEL_KEY = "__magus_cancel__";

static void setCancelHandle(lua_State *L, uintptr_t handle) {
    lua_pushlightuserdata(L, (void*)(uintptr_t)handle);
    lua_setfield(L, LUA_REGISTRYINDEX, CANCEL_KEY);
}

static uintptr_t getCancelHandle(lua_State *L) {
    lua_getfield(L, LUA_REGISTRYINDEX, CANCEL_KEY);
    void *p = lua_touserdata(L, -1);
    lj_pop(L, 1);
    return (uintptr_t)p;
}

// Hook installer — keeps the function pointer cast in C where it's valid.
extern void hookTrampoline(lua_State *L, lua_Debug *ar);
static void lj_sethook(lua_State *L, int mask, int count) {
    lua_sethook(L, hookTrampoline, mask, count);
}

// rawgeti/rawseti with int key (LuaJIT Lua 5.1 API uses int, not lua_Integer).
static void lj_rawgeti(lua_State *L, int idx, int n) { lua_rawgeti(L, idx, n); }
static void lj_rawseti(lua_State *L, int idx, int n) { lua_rawseti(L, idx, n); }
static void lj_rawgeti_reg(lua_State *L, int ref) { lua_rawgeti(L, LUA_REGISTRYINDEX, ref); }

// --- Debug API wrappers used by Frames/Locals/Upvalues/CallDepth ---

// lj_dbg_frame holds the subset of lua_Debug fields the Go side reads.
typedef struct lj_dbg_frame {
    int    valid;        // 1 if GetStack/GetInfo succeeded, else 0
    int    current_line; // -1 if unknown
    char   source[512];
    char   short_src[64];
    char   what[16];
    char   name[128];
} lj_dbg_frame;

// lj_getframe fills out with frame info at the given call level.
// Returns 1 if a frame at that level exists, 0 otherwise.
static int lj_getframe(lua_State *L, int level, lj_dbg_frame *out) {
    lua_Debug ar;
    memset(&ar, 0, sizeof(ar));
    out->valid = 0;
    if (lua_getstack(L, level, &ar) == 0) {
        return 0;
    }
    if (lua_getinfo(L, "Snl", &ar) == 0) {
        return 0;
    }
    out->valid = 1;
    out->current_line = ar.currentline;
    if (ar.source) {
        size_t n = strlen(ar.source);
        if (n >= sizeof(out->source)) n = sizeof(out->source) - 1;
        memcpy(out->source, ar.source, n);
        out->source[n] = 0;
    }
    if (ar.short_src[0]) {
        size_t n = strlen(ar.short_src);
        if (n >= sizeof(out->short_src)) n = sizeof(out->short_src) - 1;
        memcpy(out->short_src, ar.short_src, n);
        out->short_src[n] = 0;
    }
    if (ar.what) {
        size_t n = strlen(ar.what);
        if (n >= sizeof(out->what)) n = sizeof(out->what) - 1;
        memcpy(out->what, ar.what, n);
        out->what[n] = 0;
    }
    if (ar.name) {
        size_t n = strlen(ar.name);
        if (n >= sizeof(out->name)) n = sizeof(out->name) - 1;
        memcpy(out->name, ar.name, n);
        out->name[n] = 0;
    }
    return 1;
}

// lj_getlocal_at retrieves the n-th local at the given frame level.
// On success, copies the name to out_name (cap bytes) and pushes the value
// onto the stack, returning 1. On failure returns 0 and pushes nothing.
static int lj_getlocal_at(lua_State *L, int level, int n, char *out_name, int cap) {
    lua_Debug ar;
    memset(&ar, 0, sizeof(ar));
    if (lua_getstack(L, level, &ar) == 0) return 0;
    const char *name = lua_getlocal(L, &ar, n);
    if (name == NULL) return 0;
    int len = (int)strlen(name);
    if (len >= cap) len = cap - 1;
    memcpy(out_name, name, len);
    out_name[len] = 0;
    return 1;
}

// lj_getupvalue_at retrieves the n-th upvalue of the function at the given
// frame level. Pushes the value on success. Caller has set out_name buffer.
static int lj_getupvalue_at(lua_State *L, int level, int n, char *out_name, int cap) {
    lua_Debug ar;
    memset(&ar, 0, sizeof(ar));
    if (lua_getstack(L, level, &ar) == 0) return 0;
    if (lua_getinfo(L, "f", &ar) == 0) return 0; // pushes function
    const char *name = lua_getupvalue(L, -1, n);
    if (name == NULL) {
        lua_pop(L, 1); // pop function
        return 0;
    }
    int len = (int)strlen(name);
    if (len >= cap) len = cap - 1;
    memcpy(out_name, name, len);
    out_name[len] = 0;
    // Stack: [function, upvalue]. Move upvalue underneath function, then pop function.
    lua_insert(L, -2);
    lua_pop(L, 1);
    return 1;
}

// lj_calldepth counts the number of active Lua frames.
static int lj_calldepth(lua_State *L) {
    lua_Debug ar;
    int n = 0;
    for (; lua_getstack(L, n, &ar); n++) {}
    return n;
}

// lj_sethook_mask updates the hook mask for an existing state. The hook
// callback itself (hookTrampoline) stays the same; the mask determines which
// events fire it. count is only used for LUA_MASKCOUNT.
static void lj_sethook_mask(lua_State *L, int mask, int count) {
    lua_sethook(L, hookTrampoline, mask, count);
}

// Mask constants exposed to Go. Declared as an enum (not `static const int`)
// so cgo folds them to compile-time constants; a file-static would be elided
// by gcc -O2 as unused within the C translation unit, breaking the link.
enum {
    LJ_MASKLINE  = LUA_MASKLINE,
    LJ_MASKCALL  = LUA_MASKCALL,
    LJ_MASKRET   = LUA_MASKRET,
    LJ_MASKCOUNT = LUA_MASKCOUNT,
    LJ_HOOKLINE  = LUA_HOOKLINE,
    LJ_HOOKCALL  = LUA_HOOKCALL,
    LJ_HOOKRET   = LUA_HOOKRET,
};
*/
import "C"

import (
	"context"
	"fmt"
	goruntime "runtime"
	"runtime/cgo"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/egladman/magus/internal/interp/engine"
	lua "github.com/egladman/magus/internal/interp/engine/lua"
)

// liveStates maps uintptr(lua_State pointer) → *atomic.Bool (closed flag).
// It lets finalizers skip luaL_unref on states that have already been lua_close'd.
var liveStates sync.Map

// stepHooks maps uintptr(lua_State pointer) → *stepHookEntry. hookTrampoline
// looks this up on every event to decide whether to invoke a Go callback.
//
// We use sync.Map so concurrent access (a step hook firing while Go sets a
// new one) is safe even though a given lua_State is single-threaded.
var stepHooks sync.Map

type stepHookEntry struct {
	mask C.int
	cb   func(engine.StepEvent, engine.Frame)
}

func init() {
	engine.Register("luajit", &ljBackend{})
}

type ljBackend struct{}

func (b *ljBackend) ID() string { return "luajit-2.1" }

func (b *ljBackend) NewSession(ctx context.Context) (engine.Session, error) {
	goruntime.LockOSThread()
	L := C.luaL_newstate()
	if L == nil {
		return nil, fmt.Errorf("luajit: luaL_newstate returned nil")
	}
	safeOpenLibs(L)

	if ctx == nil {
		ctx = context.Background()
	}
	closed := new(atomic.Bool)
	liveStates.Store(uintptr(unsafe.Pointer(L)), closed)
	s := &ljState{L: L, closed: closed}
	if ctx.Done() != nil {
		s.setContext(ctx)
	}
	return s, nil
}

// safeOpenLibs opens standard libraries then removes dangerous globals.
// The JIT engine is disabled so that concurrent lua_States in separate
// goroutines do not corrupt each other through LuaJIT's shared JIT state.
func safeOpenLibs(L *C.lua_State) { //nolint:gocritic // L is the canonical lua_State identifier in the LuaJIT C API
	C.luaL_openlibs(L)
	C.luaJIT_setmode(L, 0, C.LUAJIT_MODE_ENGINE|C.LUAJIT_MODE_OFF)

	for _, name := range []string{"ffi", "debug"} {
		cname := C.CString(name)
		C.lua_pushnil(L)
		C.lj_setglobal(L, cname)
		C.free(unsafe.Pointer(cname))
	}

	cosName := C.CString("os")
	C.lj_getglobal(L, cosName)
	C.free(unsafe.Pointer(cosName))
	if C.lua_type(L, -1) == C.LUA_TTABLE {
		for _, fn := range []string{"execute", "exit", "remove", "rename", "tmpname"} {
			cfn := C.CString(fn)
			C.lua_pushnil(L)
			C.lua_setfield(L, -2, cfn)
			C.free(unsafe.Pointer(cfn))
		}
	}
	C.lj_pop(L, 1)

	cioName := C.CString("io")
	C.lj_getglobal(L, cioName)
	C.free(unsafe.Pointer(cioName))
	if C.lua_type(L, -1) == C.LUA_TTABLE {
		for _, fn := range []string{"popen"} {
			cfn := C.CString(fn)
			C.lua_pushnil(L)
			C.lua_setfield(L, -2, cfn)
			C.free(unsafe.Pointer(cfn))
		}
	}
	C.lj_pop(L, 1)

	cpkgName := C.CString("package")
	C.lj_getglobal(L, cpkgName)
	C.free(unsafe.Pointer(cpkgName))
	if C.lua_type(L, -1) == C.LUA_TTABLE {
		for _, fn := range []string{"loadlib", "cpath"} {
			cfn := C.CString(fn)
			C.lua_pushnil(L)
			C.lua_setfield(L, -2, cfn)
			C.free(unsafe.Pointer(cfn))
		}
	}
	C.lj_pop(L, 1)

	for _, name := range []string{"loadfile", "dofile"} {
		cname := C.CString(name)
		C.lua_pushnil(L)
		C.lj_setglobal(L, cname)
		C.free(unsafe.Pointer(cname))
	}
}

// --- State ---

type ljState struct {
	L         *C.lua_State
	closed    *atomic.Bool
	fnMu      sync.Mutex
	fnHandles []cgo.Handle // GoFunc handles; deleted in Close, not in finalizer
}

func (s *ljState) Close() error {
	if s.closed != nil {
		s.closed.Store(true)
		liveStates.Delete(uintptr(unsafe.Pointer(s.L)))
		stepHooks.Delete(uintptr(unsafe.Pointer(s.L)))
	}
	s.fnMu.Lock()
	for _, h := range s.fnHandles {
		h.Delete()
	}
	s.fnHandles = nil
	s.fnMu.Unlock()
	// Free the context handle currently planted on L, if any. The handle is
	// tracked on L (not the struct) because callbacks run on freshly-wrapped
	// ljState values that share the same L; see setContext.
	if ch := uintptr(C.getCancelHandle(s.L)); ch != 0 {
		cgo.Handle(ch).Delete()
	}
	C.lua_close(s.L)
	goruntime.UnlockOSThread()
	return nil
}

// Context returns the context currently planted on L, or context.Background()
// when none is set.
func (s *ljState) Context() context.Context {
	if ch := uintptr(C.getCancelHandle(s.L)); ch != 0 {
		if c, ok := cgo.Handle(ch).Value().(context.Context); ok {
			return c
		}
	}
	return context.Background()
}

// SetContext plants ctx on L so Go bindings invoked from this VM observe its
// values and cancellation. host.OsWithEnv relies on this to thread
// sh.with_env overrides into nested sh.* calls (luaCallback.Call sets the
// callback's context here and restores the prior one afterward). A nil ctx is
// treated as context.Background().
func (s *ljState) SetContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.setContext(ctx)
}

// setContext plants ctx's handle on L, freeing any handle already there so
// exactly one is live at a time (it may have been planted by a different
// ljState sharing this L, e.g. inside a callback). The cancellation hook is
// installed only for a cancellable ctx; a values-only ctx (sh.with_env) still
// reaches bindings via the handle without paying the hook's per-instruction cost.
func (s *ljState) setContext(ctx context.Context) {
	if prev := uintptr(C.getCancelHandle(s.L)); prev != 0 {
		cgo.Handle(prev).Delete()
	}
	ch := cgo.NewHandle(ctx)
	C.setCancelHandle(s.L, C.uintptr_t(ch))
	if ctx.Done() != nil {
		C.lj_sethook(s.L, C.LUA_MASKCOUNT|C.LUA_MASKCALL|C.LUA_MASKRET, 1000)
	}
}

func (s *ljState) SetGlobal(name string, v engine.Value) {
	s.push(v)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.lj_setglobal(s.L, cname)
}

func (s *ljState) GetGlobal(name string) engine.Value {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.lj_getglobal(s.L, cname)
	v := s.stackToValue(-1)
	C.lj_pop(s.L, 1)
	return v
}

func (s *ljState) Push(v engine.Value) { s.push(v) }

func (s *ljState) Pop(n int) { C.lj_pop(s.L, C.int(n)) }

func (s *ljState) Get(idx int) engine.Value { return s.stackToValue(idx) }

func (s *ljState) GetTop() int { return int(C.lua_gettop(s.L)) }

func (s *ljState) NewTable() engine.Table {
	C.lua_createtable(s.L, 0, 0)
	t := &ljTable{s: s, ref: s.popRef()}
	return t
}

func (s *ljState) NewFunction(fn lua.GoFunc) engine.Value {
	h := cgo.NewHandle(fn)
	C.pushGoFunc(s.L, C.uintptr_t(h))
	// Register h so Close() can delete it. The finalizer must NOT delete h
	// because the Lua closure (which holds the handle uintptr as an upvalue)
	// may still be callable after the Go wrapper is GC'd.
	s.fnMu.Lock()
	s.fnHandles = append(s.fnHandles, h)
	s.fnMu.Unlock()
	v := &ljValue{s: s, ref: s.popRef()}
	return v
}

func (s *ljState) CheckString(n int) string {
	cs := C.lj_checkstring(s.L, C.int(n))
	return C.GoString(cs)
}

func (s *ljState) CheckNumber(n int) float64 {
	return float64(C.luaL_checknumber(s.L, C.int(n)))
}

func (s *ljState) CheckInt(n int) int {
	return int(C.luaL_checkinteger(s.L, C.int(n)))
}

func (s *ljState) CheckTable(n int) engine.Table {
	C.luaL_checktype(s.L, C.int(n), C.LUA_TTABLE)
	C.lua_pushvalue(s.L, C.int(n))
	t := &ljTable{s: s, ref: s.popRef()}
	return t
}

func (s *ljState) CheckFunction(n int) engine.Value {
	C.luaL_checktype(s.L, C.int(n), C.LUA_TFUNCTION)
	C.lua_pushvalue(s.L, C.int(n))
	v := &ljValue{s: s, ref: s.popRef()}
	return v
}

func (s *ljState) CheckAny(n int) engine.Value {
	C.luaL_checkany(s.L, C.int(n))
	return s.stackToValue(n)
}

// ljRaise is the sentinel panicked by RaiseError/ArgError. goFuncTrampoline
// recovers it, pushes the message onto L, and returns the -1 signal so the C
// dispatcher (goFuncDispatch) raises lua_error from a pure-C frame. The actual
// longjmp must not originate inside the Go trampoline frame (see goFuncDispatch).
type ljRaise struct {
	msg   string
	argN  int  // 0 = plain RaiseError; >0 = ArgError for argument argN
	isArg bool // distinguishes ArgError (formats "bad argument #n ...") from RaiseError
}

func (s *ljState) RaiseError(format string, args ...any) {
	panic(ljRaise{msg: fmt.Sprintf(format, args...)})
}

func (s *ljState) ArgError(n int, msg string) {
	panic(ljRaise{msg: msg, argN: n, isArg: true})
}

func (s *ljState) LoadString(code string) (engine.Value, error) {
	ccode := C.CString(code)
	defer C.free(unsafe.Pointer(ccode))
	if rc := C.luaL_loadstring(s.L, ccode); rc != 0 {
		return nil, s.popError()
	}
	v := &ljValue{s: s, ref: s.popRef()}
	return v, nil
}

func (s *ljState) DoString(code string) error {
	ccode := C.CString(code)
	defer C.free(unsafe.Pointer(ccode))
	if rc := C.luaL_loadstring(s.L, ccode); rc != 0 {
		return s.popError()
	}
	if rc := C.lua_pcall(s.L, 0, C.LUA_MULTRET, 0); rc != 0 {
		return s.popError()
	}
	return nil
}

func (s *ljState) Call(p engine.CallParams, args ...engine.Value) error {
	s.push(p.Fn)
	for _, a := range args {
		s.push(a)
	}
	nargs := C.int(len(args))
	nret := C.int(p.NRet)
	if p.Protect {
		if rc := C.lua_pcall(s.L, nargs, nret, 0); rc != 0 {
			return s.popError()
		}
		return nil
	}
	C.lua_call(s.L, nargs, nret)
	return nil
}

// popRef pops the top of the stack into the Lua registry and returns its ref.
func (s *ljState) popRef() C.int {
	return C.luaL_ref(s.L, C.LUA_REGISTRYINDEX)
}

// pushRef pushes the registry value for ref onto the stack.
func (s *ljState) pushRef(ref C.int) {
	C.lj_rawgeti_reg(s.L, ref)
}

// unref releases a registry reference. It is a no-op if the lua_State has
// already been closed (s.closed nil or true) — the Lua memory was freed by
// lua_close and calling luaL_unref would be a use-after-free.
func (s *ljState) unref(ref C.int) {
	if s.closed == nil || s.closed.Load() {
		return
	}
	C.luaL_unref(s.L, C.LUA_REGISTRYINDEX, ref)
}

// popError pops an error string from the stack and returns it as a Go error.
func (s *ljState) popError() error {
	cs := C.lua_tolstring(s.L, -1, nil)
	var msg string
	if cs != nil {
		msg = C.GoString(cs)
	}
	C.lj_pop(s.L, 1)
	return fmt.Errorf("%s", msg)
}

// push pushes an engine.Value onto the Lua stack.
//
//nolint:revive // confusing-naming: Push is the exported engine interface method; push is its unexported implementation (idiomatic wrapper pair).
func (s *ljState) push(v engine.Value) {
	if v == nil || v.IsNil() {
		C.lua_pushnil(s.L)
		return
	}
	switch tv := v.(type) {
	case *ljValue:
		s.pushRef(tv.ref)
		return
	case *ljTable:
		s.pushRef(tv.ref)
		return
	}
	if str, ok := v.AsString(); ok {
		cs := C.CString(str)
		defer C.free(unsafe.Pointer(cs))
		C.lua_pushlstring(s.L, cs, C.size_t(len(str)))
		return
	}
	if n, ok := v.AsNumber(); ok {
		C.lua_pushnumber(s.L, C.lua_Number(n))
		return
	}
	if v.AsBool() {
		C.lua_pushboolean(s.L, 1)
	} else {
		C.lua_pushboolean(s.L, 0)
	}
}

// stackToValue reads the Lua value at stack index idx and returns an engine.Value.
func (s *ljState) stackToValue(idx int) engine.Value {
	switch C.lua_type(s.L, C.int(idx)) {
	case C.LUA_TNIL, C.LUA_TNONE:
		return engine.NilValue
	case C.LUA_TBOOLEAN:
		b := C.lua_toboolean(s.L, C.int(idx)) != 0
		return engine.BoolValue(b)
	case C.LUA_TNUMBER:
		n := float64(C.lua_tonumber(s.L, C.int(idx)))
		return engine.NumberValue(n)
	case C.LUA_TSTRING:
		cs := C.lua_tolstring(s.L, C.int(idx), nil)
		return engine.StringValue(C.GoString(cs))
	case C.LUA_TTABLE:
		C.lua_pushvalue(s.L, C.int(idx))
		t := &ljTable{s: s, ref: s.popRef()}
		return t
	default:
		// Function, userdata, thread, light userdata.
		C.lua_pushvalue(s.L, C.int(idx))
		v := &ljValue{s: s, ref: s.popRef()}
		return v
	}
}

// --- Value ---

// ljValue holds a Lua registry reference to an arbitrary Lua value.
type ljValue struct {
	s       *ljState
	ref     C.int
	unrefed bool
}

func (v *ljValue) unref() {
	if !v.unrefed {
		v.s.unref(v.ref)
		v.unrefed = true
	}
}

func (v *ljValue) luaType() C.int {
	v.s.pushRef(v.ref)
	t := C.lua_type(v.s.L, -1)
	C.lj_pop(v.s.L, 1)
	return t
}

func (v *ljValue) IsNil() bool { return v.luaType() == C.LUA_TNIL }
func (v *ljValue) String() string {
	v.s.pushRef(v.ref)
	cs := C.lua_tolstring(v.s.L, -1, nil)
	C.lj_pop(v.s.L, 1)
	if cs == nil {
		return ""
	}
	return C.GoString(cs)
}

func (v *ljValue) AsString() (string, bool) {
	v.s.pushRef(v.ref)
	if C.lua_type(v.s.L, -1) != C.LUA_TSTRING {
		C.lj_pop(v.s.L, 1)
		return "", false
	}
	cs := C.lua_tolstring(v.s.L, -1, nil)
	s := C.GoString(cs)
	C.lj_pop(v.s.L, 1)
	return s, true
}

func (v *ljValue) AsNumber() (float64, bool) {
	v.s.pushRef(v.ref)
	if C.lua_type(v.s.L, -1) != C.LUA_TNUMBER {
		C.lj_pop(v.s.L, 1)
		return 0, false
	}
	n := float64(C.lua_tonumber(v.s.L, -1))
	C.lj_pop(v.s.L, 1)
	return n, true
}

func (v *ljValue) AsBool() bool {
	v.s.pushRef(v.ref)
	b := C.lua_toboolean(v.s.L, -1) != 0
	C.lj_pop(v.s.L, 1)
	return b
}

func (v *ljValue) AsTable() (engine.Table, bool) {
	v.s.pushRef(v.ref)
	if C.lua_type(v.s.L, -1) != C.LUA_TTABLE {
		C.lj_pop(v.s.L, 1)
		return nil, false
	}
	t := &ljTable{s: v.s, ref: v.s.popRef()}
	return t, true
}

func (v *ljValue) AsFunction() (engine.Value, bool) {
	return v, v.luaType() == C.LUA_TFUNCTION
}

// --- Table ---

// ljTable holds a Lua registry reference to a Lua table.
type ljTable struct {
	s       *ljState
	ref     C.int
	unrefed bool
}

func (t *ljTable) unref() {
	if !t.unrefed {
		t.s.unref(t.ref)
		t.unrefed = true
	}
}

func (t *ljTable) IsNil() bool                      { return false }
func (t *ljTable) String() string                   { return "table" }
func (t *ljTable) AsString() (string, bool)         { return "", false }
func (t *ljTable) AsNumber() (float64, bool)        { return 0, false }
func (t *ljTable) AsBool() bool                     { return true }
func (t *ljTable) AsFunction() (engine.Value, bool) { return nil, false }
func (t *ljTable) AsTable() (engine.Table, bool)    { return t, true }

func (t *ljTable) RawSetString(key string, v engine.Value) {
	t.s.pushRef(t.ref)
	ckey := C.CString(key)
	defer C.free(unsafe.Pointer(ckey))
	t.s.push(v)
	C.lua_setfield(t.s.L, -2, ckey)
	C.lj_pop(t.s.L, 1)
}

func (t *ljTable) RawGetString(key string) engine.Value {
	t.s.pushRef(t.ref)
	ckey := C.CString(key)
	defer C.free(unsafe.Pointer(ckey))
	C.lua_getfield(t.s.L, -1, ckey)
	v := t.s.stackToValue(-1)
	C.lj_pop(t.s.L, 2)
	return v
}

func (t *ljTable) RawSetInt(key int, v engine.Value) {
	t.s.pushRef(t.ref)
	t.s.push(v)
	C.lj_rawseti(t.s.L, -2, C.int(key))
	C.lj_pop(t.s.L, 1)
}

func (t *ljTable) RawGetInt(key int) engine.Value {
	t.s.pushRef(t.ref)
	C.lj_rawgeti(t.s.L, -1, C.int(key))
	v := t.s.stackToValue(-1)
	C.lj_pop(t.s.L, 2)
	return v
}

func (t *ljTable) ForEach(fn func(k, v engine.Value)) {
	t.s.pushRef(t.ref)
	C.lua_pushnil(t.s.L)
	for C.lua_next(t.s.L, -2) != 0 {
		k := t.s.stackToValue(-2)
		v := t.s.stackToValue(-1)
		C.lj_pop(t.s.L, 1)
		fn(k, v)
	}
	C.lj_pop(t.s.L, 1)
}

func (t *ljTable) Len() int {
	t.s.pushRef(t.ref)
	n := int(C.lua_objlen(t.s.L, -1))
	C.lj_pop(t.s.L, 1)
	return n
}

// --- CGO trampolines ---

// goFuncTrampoline is the single C function used for all registered Go
// closures. The Go function handle lives in upvalue 1.
//
//export goFuncTrampoline
func goFuncTrampoline(L *C.lua_State) (nret C.int) { //nolint:gocritic // L is the canonical lua_State identifier in the LuaJIT C API
	p := C.lua_touserdata(L, C.lj_upvalueindex(1))
	h := cgo.Handle(uintptr(p))
	fn := h.Value().(lua.GoFunc) //nolint:forcetypeassert // the handle is only ever created from a lua.GoFunc in registerGoFunc

	// Recover the VM's context from the C-side cancel handle that
	// setContext plants on L. When NewState was given a non-cancellable
	// ctx no handle is set; fall back to Background.
	ctx := context.Background()
	if ch := uintptr(C.getCancelHandle(L)); ch != 0 {
		if c, ok := cgo.Handle(ch).Value().(context.Context); ok {
			ctx = c
		}
	}

	s := &ljState{L: L}
	if v, ok := liveStates.Load(uintptr(unsafe.Pointer(L))); ok {
		s.closed = v.(*atomic.Bool) //nolint:forcetypeassert // liveStates only ever stores *atomic.Bool
	}

	// RaiseError/ArgError panic with an ljRaise sentinel rather than calling
	// lua_error directly: the longjmp must fire from goFuncDispatch's C frame,
	// not from here (a cgo-exported Go frame). Recover it, push the formatted
	// message onto L, and return -1 to signal goFuncDispatch to raise it. Any
	// other panic is genuinely unexpected and is left to propagate.
	defer func() {
		if rec := recover(); rec != nil {
			rr, ok := rec.(ljRaise)
			if !ok {
				panic(rec)
			}
			msg := rr.msg
			if rr.isArg {
				// Mirror luaL_argerror's wording so ArgError reads the same as
				// the standard Lua C API error.
				msg = fmt.Sprintf("bad argument #%d (%s)", rr.argN, rr.msg)
			}
			cmsg := C.CString(msg)
			C.lua_pushstring(L, cmsg)
			C.free(unsafe.Pointer(cmsg))
			nret = -1
		}
	}()

	return C.int(fn(ctx, s))
}

// hookTrampoline is called by LuaJIT on every N instructions / call / return /
// line, depending on the active mask. It serves two purposes:
//  1. context-cancellation: if the bound context is cancelled it pushes an
//     error and longjmps out via lua_error.
//  2. step debugging: if a step hook is registered for this state, dispatch
//     it (with the current frame and event kind) before returning.
//
//export hookTrampoline
func hookTrampoline(L *C.lua_State, ar *C.lua_Debug) { //nolint:gocritic // L is the canonical lua_State identifier in the LuaJIT C API
	// Context cancellation is checked first: if the context is already done
	// there is no point entering a REPL — longjmp out immediately so the
	// step callback never runs on a cancelled context.
	handle := uintptr(C.getCancelHandle(L))
	if handle != 0 {
		if ctx, ok := cgo.Handle(handle).Value().(context.Context); ok && ctx.Err() != nil {
			cmsg := C.CString("context cancelled")
			C.lua_pushstring(L, cmsg)
			C.free(unsafe.Pointer(cmsg))
			C.lua_error(L)
		}
	}

	if entry, ok := stepHooks.Load(uintptr(unsafe.Pointer(L))); ok {
		e := entry.(*stepHookEntry) //nolint:forcetypeassert // stepHooks only ever stores *stepHookEntry
		ev := stepEventFromAr(ar)
		// Build a frame snapshot at level 0 (the active frame).
		var dbgf C.lj_dbg_frame
		if C.lj_getframe(L, 0, &dbgf) != 0 {
			frame := engine.Frame{
				Source:      C.GoString(&dbgf.source[0]),
				ShortSrc:    C.GoString(&dbgf.short_src[0]),
				CurrentLine: int(dbgf.current_line),
				Name:        C.GoString(&dbgf.name[0]),
				What:        C.GoString(&dbgf.what[0]),
			}
			e.cb(ev, frame)
		}
	}
}

// stepEventFromAr maps LuaJIT's event code (in ar.event) to our StepEvent.
func stepEventFromAr(ar *C.lua_Debug) engine.StepEvent {
	switch ar.event {
	case C.LJ_HOOKLINE:
		return engine.StepLine
	case C.LJ_HOOKCALL:
		return engine.StepCall
	case C.LJ_HOOKRET:
		return engine.StepReturn
	}
	return engine.StepLine
}

// Frames implements engine.DebugReader by walking via lua_getstack/lua_getinfo.
func (s *ljState) Frames() []engine.Frame {
	var frames []engine.Frame
	for level := 0; ; level++ {
		var dbgf C.lj_dbg_frame
		if C.lj_getframe(s.L, C.int(level), &dbgf) == 0 {
			break
		}
		what := C.GoString(&dbgf.what[0])
		if what == "C" {
			continue // skip host frames
		}
		frames = append(frames, engine.Frame{
			Source:      C.GoString(&dbgf.source[0]),
			ShortSrc:    C.GoString(&dbgf.short_src[0]),
			CurrentLine: int(dbgf.current_line),
			Name:        C.GoString(&dbgf.name[0]),
			What:        what,
		})
	}
	return frames
}

// Locals returns the named locals at the requested Lua frame.
func (s *ljState) Locals(level int) map[string]engine.Value {
	out := map[string]engine.Value{}
	rawLevel, ok := s.luaFrameToRaw(level)
	if !ok {
		return out
	}
	var nameBuf [128]C.char
	for i := 1; ; i++ {
		if C.lj_getlocal_at(s.L, C.int(rawLevel), C.int(i), &nameBuf[0], C.int(len(nameBuf))) == 0 {
			break
		}
		name := C.GoString(&nameBuf[0])
		// Convert the value left on the stack, then pop.
		val := s.stackToValue(-1)
		C.lj_pop(s.L, 1)
		if len(name) > 0 && name[0] == '(' {
			continue
		}
		out[name] = val
	}
	return out
}

// Upvalues returns the upvalues of the function at the requested Lua frame.
func (s *ljState) Upvalues(level int) map[string]engine.Value {
	out := map[string]engine.Value{}
	rawLevel, ok := s.luaFrameToRaw(level)
	if !ok {
		return out
	}
	var nameBuf [128]C.char
	for i := 1; ; i++ {
		if C.lj_getupvalue_at(s.L, C.int(rawLevel), C.int(i), &nameBuf[0], C.int(len(nameBuf))) == 0 {
			break
		}
		name := C.GoString(&nameBuf[0])
		val := s.stackToValue(-1)
		C.lj_pop(s.L, 1)
		out[name] = val
	}
	return out
}

// CallDepth returns the number of active Lua frames (including host frames).
func (s *ljState) CallDepth() int {
	return int(C.lj_calldepth(s.L))
}

// luaFrameToRaw maps a 0-based Lua-only frame index to the raw level value
// that lua_getstack expects, skipping host (C) frames so it matches Frames().
func (s *ljState) luaFrameToRaw(level int) (int, bool) {
	luaIdx := 0
	for raw := 0; ; raw++ {
		var dbgf C.lj_dbg_frame
		if C.lj_getframe(s.L, C.int(raw), &dbgf) == 0 {
			return 0, false
		}
		what := C.GoString(&dbgf.what[0])
		if what == "C" {
			continue
		}
		if luaIdx == level {
			return raw, true
		}
		luaIdx++
	}
}

// SetStepHook implements engine.Stepper.
func (s *ljState) SetStepHook(mask engine.StepMask, cb func(engine.StepEvent, engine.Frame)) {
	cmask := C.int(0)
	if mask&engine.MaskLine != 0 {
		cmask |= C.LJ_MASKLINE
	}
	if mask&engine.MaskCall != 0 {
		cmask |= C.LJ_MASKCALL
	}
	if mask&engine.MaskReturn != 0 {
		cmask |= C.LJ_MASKRET
	}
	// Only add MASKCOUNT for cancellation polling; respect the caller's mask
	// for which events (line/call/return) actually trigger the step callback.
	stepHooks.Store(uintptr(unsafe.Pointer(s.L)), &stepHookEntry{mask: cmask, cb: cb})
	C.lj_sethook_mask(s.L, cmask|C.LJ_MASKCOUNT, 1000)
}

// Drivers implements engine.DriversProvider.
func (s *ljState) Drivers() []engine.ReplDriver {
	return []engine.ReplDriver{lua.NewDriver(s)}
}

// ClearStepHook implements engine.Stepper.
func (s *ljState) ClearStepHook() {
	stepHooks.Delete(uintptr(unsafe.Pointer(s.L)))
	// Restore the default mask: cancellation-polling only.
	C.lj_sethook_mask(s.L, C.LJ_MASKCOUNT|C.LJ_MASKCALL|C.LJ_MASKRET, 1000)
}
