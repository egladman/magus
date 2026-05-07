package luagen

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/interp/engine"
	lua "github.com/egladman/magus/internal/interp/engine/lua"
	"github.com/egladman/magus/internal/std"
)

// luaCallback is the lua-side implementation of std.Callback. It wraps a
// Session + function Value pair captured at arg-decode time and invokes the
// function via Session.Call when an Impl asks the std.Callback to fire.
//
// Args passed by Impls are converted through anyToValue per call.
type luaCallback struct {
	r  lua.Session
	fn engine.Value
}

// newLuaCallback is the constructor used by generated trampolines for
// TypeFunc arguments. Returned as std.Callback so Impls see the interface.
func newLuaCallback(r lua.Session, fn engine.Value) std.Callback {
	return &luaCallback{r: r, fn: fn}
}

func (c *luaCallback) Call(ctx context.Context, args ...any) ([]any, error) {
	// Propagate ctx into the VM for the duration of the callback so nested host
	// bindings observe context values the calling Impl set — e.g. os.exec_sh
	// inside os.with_env must see the with_env overrides the Impl placed on ctx.
	// Restore the prior context afterward. Backends without per-call context
	// (the cgo luajit engine) simply run the callback with their existing ctx.
	if cs, ok := c.r.(interface {
		Context() context.Context
		SetContext(context.Context)
	}); ok && ctx != nil {
		prev := cs.Context()
		cs.SetContext(ctx)
		defer cs.SetContext(prev)
	}
	luaArgs := make([]engine.Value, len(args))
	for i, a := range args {
		luaArgs[i] = anyToValue(c.r, a)
	}
	if err := c.r.Call(engine.CallParams{Fn: c.fn, NRet: 1, Protect: true}, luaArgs...); err != nil {
		return nil, err
	}
	// Capture the single return value so predicate callbacks (e.g. arg.index_func)
	// can report a result; void callbacks return nil, which Impls ignore.
	ret := c.r.Get(-1)
	c.r.Pop(1)
	return []any{valueToAny(ret)}, nil
}

// pushAnyMap pushes m onto r's stack as a Lua table with string keys. Value
// types are resolved at runtime via anyToValue.
func pushAnyMap(r lua.Session, m map[string]any) {
	tbl := r.NewTable()
	for k, v := range m {
		tbl.RawSetString(k, anyToValue(r, v))
	}
	r.Push(tbl)
}

// anyToValue converts a Go value of dynamic type into a Session Value. Falls
// back to the Sprint form for types not natively representable in Lua.
func anyToValue(r lua.Session, v any) engine.Value {
	switch x := v.(type) {
	case nil:
		return engine.NilValue
	case string:
		return engine.StringValue(x)
	case bool:
		return engine.BoolValue(x)
	case int:
		return engine.NumberValue(float64(x))
	case int64:
		return engine.NumberValue(float64(x))
	case float64:
		return engine.NumberValue(x)
	case []string:
		tbl := r.NewTable()
		for i, s := range x {
			tbl.RawSetInt(i+1, engine.StringValue(s))
		}
		return tbl
	case []any:
		tbl := r.NewTable()
		for i, vv := range x {
			tbl.RawSetInt(i+1, anyToValue(r, vv))
		}
		return tbl
	case map[string]any:
		tbl := r.NewTable()
		for k, vv := range x {
			tbl.RawSetString(k, anyToValue(r, vv))
		}
		return tbl
	}
	return engine.StringValue(fmt.Sprintf("%v", v))
}

// valueToAny converts a Lua Value into a Go value suitable for json.Marshal.
// Tables with consecutive integer keys starting at 1 are treated as arrays
// ([]any); tables with string keys become map[string]any.
func valueToAny(v engine.Value) any {
	if v.IsNil() {
		return nil
	}
	if s, ok := v.AsString(); ok {
		return s
	}
	if n, ok := v.AsNumber(); ok {
		return n
	}
	if tbl, ok := v.AsTable(); ok {
		// Sniff array vs object: an array has sequential integer keys 1..n.
		n := tbl.Len()
		isArray := n > 0
		if isArray {
			for i := 1; i <= n; i++ {
				if tbl.RawGetInt(i).IsNil() {
					isArray = false
					break
				}
			}
		}
		if isArray {
			arr := make([]any, n)
			for i := 1; i <= n; i++ {
				arr[i-1] = valueToAny(tbl.RawGetInt(i))
			}
			return arr
		}
		m := map[string]any{}
		tbl.ForEach(func(k, vv engine.Value) {
			if key, ok := k.AsString(); ok {
				m[key] = valueToAny(vv)
			}
		})
		return m
	}
	return v.AsBool()
}

// AnyToValue converts a Go value into a Lua Session Value. Exported twin of the
// internal anyToValue, for callers outside this package (the spell function-op
// invoker marshalling req.Params into a handler's input table). Mirrors
// buzzgen.AnyToValue.
func AnyToValue(r lua.Session, v any) engine.Value { return anyToValue(r, v) }

// ValueToAny converts a Lua Value into a Go value. Exported twin of the internal
// valueToAny, for marshalling a function-op handler's result back to the host.
// Mirrors buzzgen.ValueToAny.
func ValueToAny(v engine.Value) any { return valueToAny(v) }
