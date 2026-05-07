// Package gopherlua implements engine.Engine using gopher-lua (pure-Go Lua 5.1).
// A blank import is sufficient to register the backend.
package gopherlua

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egladman/magus/internal/interp/engine"
	lua "github.com/egladman/magus/internal/interp/engine/lua"
	gua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

var protoCache sync.Map

func init() {
	engine.Register("gopherlua", &glEngine{})
}

type glEngine struct{}

func (b *glEngine) ID() string { return "gopherlua" }

func (b *glEngine) NewSession(ctx context.Context) (engine.Session, error) {
	L := gua.NewState()
	L.OpenLibs()
	L.SetContext(ctx)
	return &glState{L: L}, nil
}

type stepHookEntry struct {
	mask engine.StepMask
	fn   func(engine.StepEvent, engine.Frame)
}

type glState struct {
	L    *gua.LState
	hook atomic.Pointer[stepHookEntry] // nil when no hook installed
}

func (s *glState) Close() error {
	s.L.Close()
	return nil
}

// SetContext updates the VM's context; pool calls this before each job so dispatch sees the current budget.
func (s *glState) SetContext(ctx context.Context) { s.L.SetContext(ctx) }

func (s *glState) Context() context.Context { return s.L.Context() }

func (s *glState) SetGlobal(name string, v engine.Value) {
	s.L.SetGlobal(name, toLua(v))
}

func (s *glState) GetGlobal(name string) engine.Value {
	return &glValue{v: s.L.GetGlobal(name)}
}

func (s *glState) Push(v engine.Value) {
	s.L.Push(toLua(v))
}

func (s *glState) Pop(n int) {
	s.L.Pop(n)
}

func (s *glState) Get(idx int) engine.Value {
	return &glValue{v: s.L.Get(idx)}
}

func (s *glState) GetTop() int {
	return s.L.GetTop()
}

func (s *glState) NewTable() engine.Table {
	return &glTable{t: s.L.NewTable()}
}

func (s *glState) NewFunction(fn lua.GoFunc) engine.Value {
	return &glValue{v: s.L.NewFunction(func(_ *gua.LState) int {
		ctx := s.L.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		return fn(ctx, s)
	})}
}

func (s *glState) CheckString(n int) string {
	return s.L.CheckString(n)
}

func (s *glState) CheckNumber(n int) float64 {
	return float64(s.L.CheckNumber(n))
}

func (s *glState) CheckInt(n int) int {
	return s.L.CheckInt(n)
}

func (s *glState) CheckTable(n int) engine.Table {
	return &glTable{t: s.L.CheckTable(n)}
}

func (s *glState) CheckFunction(n int) engine.Value {
	return &glValue{v: s.L.CheckFunction(n)}
}

func (s *glState) CheckAny(n int) engine.Value {
	return &glValue{v: s.L.CheckAny(n)}
}

func (s *glState) RaiseError(format string, args ...any) {
	s.L.RaiseError(format, args...)
}

func (s *glState) ArgError(n int, msg string) {
	s.L.ArgError(n, msg)
}

// Frames implements engine.DebugReader; level 0 is the innermost Lua frame (Go frames skipped).
func (s *glState) Frames() []engine.Frame {
	var frames []engine.Frame
	for level := 0; ; level++ {
		dbg, ok := s.L.GetStack(level)
		if !ok || dbg == nil {
			break
		}
		// Fill dbg with source / line / name info.
		if _, err := s.L.GetInfo("Snl", dbg, gua.LNil); err != nil {
			break
		}
		if dbg.What == "G" {
			continue
		}
		frames = append(frames, engine.Frame{
			Source:      dbg.Source,
			ShortSrc:    shortSource(dbg.Source),
			CurrentLine: dbg.CurrentLine,
			Name:        dbg.Name,
			What:        dbg.What,
		})
	}
	return frames
}

// Locals returns named locals at the requested Lua frame (0 = innermost, matching Frames()).
func (s *glState) Locals(level int) map[string]engine.Value {
	out := map[string]engine.Value{}
	dbg := s.lookupLuaFrame(level)
	if dbg == nil {
		return out
	}
	for i := 1; ; i++ {
		name, val := s.L.GetLocal(dbg, i)
		if name == "" {
			break
		}
		if strings.HasPrefix(name, "(") { // skip gopher-lua internal slots like "(*temporary)"
			continue
		}
		out[name] = &glValue{v: val}
	}
	return out
}

// Upvalues returns named upvalues at the requested Lua frame; empty for the main chunk.
func (s *glState) Upvalues(level int) map[string]engine.Value {
	out := map[string]engine.Value{}
	dbg := s.lookupLuaFrame(level)
	if dbg == nil {
		return out
	}
	fnVal, err := s.L.GetInfo("f", dbg, gua.LNil)
	if err != nil {
		return out
	}
	fn, ok := fnVal.(*gua.LFunction)
	if !ok {
		return out
	}
	for i := 1; ; i++ {
		name, val := s.L.GetUpvalue(fn, i)
		if name == "" {
			break
		}
		out[name] = &glValue{v: val}
	}
	return out
}

// CallDepth counts active Lua frames; used by step-over logic.
func (s *glState) CallDepth() int {
	n := 0
	for level := 0; ; level++ {
		dbg, ok := s.L.GetStack(level)
		if !ok || dbg == nil {
			break
		}
		n++
	}
	return n
}

func (s *glState) lookupLuaFrame(level int) *gua.Debug {
	luaIdx := 0
	for raw := 0; ; raw++ {
		dbg, ok := s.L.GetStack(raw)
		if !ok || dbg == nil {
			return nil
		}
		if _, err := s.L.GetInfo("Snl", dbg, gua.LNil); err != nil {
			return nil
		}
		if dbg.What == "G" {
			continue
		}
		if luaIdx == level {
			return dbg
		}
		luaIdx++
	}
}

func shortSource(src string) string {
	s := strings.TrimPrefix(src, "@")
	if len(s) > 60 {
		return "..." + s[len(s)-57:]
	}
	return s
}

func protoKey(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

func cachedProto(source, name string) (*gua.FunctionProto, error) {
	key := protoKey(source)
	if v, ok := protoCache.Load(key); ok {
		return v.(*gua.FunctionProto), nil
	}
	chunk, err := parse.Parse(strings.NewReader(source), name)
	if err != nil {
		return nil, err
	}
	proto, err := gua.Compile(chunk, name)
	if err != nil {
		return nil, err
	}
	protoCache.Store(key, proto)
	return proto, nil
}

func (s *glState) LoadString(code string) (engine.Value, error) {
	src := code
	if s.hook.Load() != nil {
		if res, err := rewriteSteps(code); err == nil {
			src = res.Rewritten
		}
	}
	proto, err := cachedProto(src, "<string>")
	if err != nil {
		return nil, err
	}
	fn := s.L.NewFunctionFromProto(proto)
	return &glValue{v: fn}, nil
}

func (s *glState) DoString(code string) error {
	fn, err := s.LoadString(code)
	if err != nil {
		return err
	}
	return s.L.CallByParam(gua.P{Fn: toLua(fn), NRet: 0, Protect: true})
}

func (s *glState) Call(p engine.CallParams, args ...engine.Value) (err error) {
	fn := toLua(p.Fn)
	luaArgs := make([]gua.LValue, len(args))
	for i, a := range args {
		luaArgs[i] = toLua(a)
	}
	// A protected call must surface a failure as a Go error, never crash the
	// process. gopher-lua's PCall recovers an error() raised from Lua code, but
	// a RaiseError raised from a Go-backed function (e.g. a magus.target.new
	// wrapper rejecting a depends_on cycle) can escape its protected frame as a
	// panic. Recover it here so the Protect contract holds regardless of how the
	// callee signalled the failure.
	if p.Protect {
		defer func() {
			if rec := recover(); rec != nil {
				if e, ok := rec.(error); ok {
					err = e
				} else {
					err = fmt.Errorf("%v", rec)
				}
			}
		}()
	}
	return s.L.CallByParam(gua.P{Fn: fn, NRet: p.NRet, Protect: p.Protect}, luaArgs...)
}

type glValue struct{ v gua.LValue }

func (gv *glValue) IsNil() bool    { return gv.v == gua.LNil || gv.v == nil }
func (gv *glValue) String() string { return gv.v.String() }
func (gv *glValue) AsString() (string, bool) {
	s, ok := gv.v.(gua.LString)
	return string(s), ok
}

func (gv *glValue) AsNumber() (float64, bool) {
	n, ok := gv.v.(gua.LNumber)
	return float64(n), ok
}
func (gv *glValue) AsBool() bool { return gua.LVAsBool(gv.v) }
func (gv *glValue) AsTable() (engine.Table, bool) {
	t, ok := gv.v.(*gua.LTable)
	if !ok {
		return nil, false
	}
	return &glTable{t: t}, true
}

func (gv *glValue) AsFunction() (engine.Value, bool) {
	_, ok := gv.v.(*gua.LFunction)
	return gv, ok
}

type glTable struct{ t *gua.LTable }

func (gt *glTable) IsNil() bool                      { return gt.t == nil }
func (gt *glTable) String() string                   { return gt.t.String() }
func (gt *glTable) AsString() (string, bool)         { return "", false }
func (gt *glTable) AsNumber() (float64, bool)        { return 0, false }
func (gt *glTable) AsBool() bool                     { return true }
func (gt *glTable) AsTable() (engine.Table, bool)    { return gt, true }
func (gt *glTable) AsFunction() (engine.Value, bool) { return nil, false }

func (gt *glTable) RawSetString(key string, v engine.Value) {
	gt.t.RawSetString(key, toLua(v))
}

func (gt *glTable) RawGetString(key string) engine.Value {
	return &glValue{v: gt.t.RawGetString(key)}
}

func (gt *glTable) RawSetInt(key int, v engine.Value) {
	gt.t.RawSetInt(key, toLua(v))
}

func (gt *glTable) RawGetInt(key int) engine.Value {
	return &glValue{v: gt.t.RawGetInt(key)}
}

func (gt *glTable) ForEach(fn func(k, v engine.Value)) {
	gt.t.ForEach(func(k, v gua.LValue) {
		fn(&glValue{v: k}, &glValue{v: v})
	})
}
func (gt *glTable) Len() int { return gt.t.Len() }

// SetStepHook implements engine.Stepper; installs the trampoline global and enables step rewriting.
//
// Unlike LuaJIT and Buzz, which use native VM hooks, gopher-lua steps via
// compile-time source instrumentation (see rewriteSteps): step points exist only
// where the rewrite injected a __magus_step_hook call. This is a deliberate
// limitation of the pure-Go backend, not a bug — stepping granularity is coarser
// than a native hook's, but the debugging surface (engine.Stepper/DebugReader) is
// otherwise the same across all three engines.
func (s *glState) SetStepHook(mask engine.StepMask, cb func(engine.StepEvent, engine.Frame)) {
	s.hook.Store(&stepHookEntry{mask: mask, fn: cb})
	s.L.SetGlobal("__magus_step_hook", s.L.NewFunction(s.stepTrampoline))
}

func (s *glState) ClearStepHook() {
	s.hook.Store(nil)
}

// stepTrampoline is the Lua-callable __magus_step_hook(line, event) injected by steprewrite.
func (s *glState) stepTrampoline(L *gua.LState) int {
	h := s.hook.Load()
	if h == nil {
		return 0
	}
	injectedLine := L.CheckInt(1)
	evStr := L.CheckString(2)

	var ev engine.StepEvent
	var evMask engine.StepMask
	switch evStr {
	case "line":
		ev = engine.StepLine
		evMask = engine.MaskLine
	case "return":
		ev = engine.StepReturn
		evMask = engine.MaskReturn
	case "call":
		ev = engine.StepCall
		evMask = engine.MaskCall
	default:
		return 0
	}

	if h.mask&evMask == 0 {
		return 0
	}

	var frame engine.Frame
	if frames := s.Frames(); len(frames) > 0 {
		frame = frames[0]
	}
	frame.CurrentLine = injectedLine // use injected line to reflect original source position

	h.fn(ev, frame)
	return 0
}

func (s *glState) Drivers() []engine.ReplDriver {
	return []engine.ReplDriver{lua.NewDriver(s)}
}

func toLua(v engine.Value) gua.LValue {
	switch gv := v.(type) {
	case *glValue:
		return gv.v
	case *glTable:
		return gv.t
	case nil:
		return gua.LNil
	}
	if v.IsNil() {
		return gua.LNil
	}
	if str, ok := v.AsString(); ok {
		return gua.LString(str)
	}
	if n, ok := v.AsNumber(); ok {
		return gua.LNumber(n)
	}
	if v.AsBool() {
		return gua.LTrue
	}
	return gua.LFalse
}
