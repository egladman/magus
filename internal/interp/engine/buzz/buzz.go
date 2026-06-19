// Package buzz adapts the standalone Buzz interpreter (magus/gopherbuzz) to
// magus's engine.Engine/engine.Session interfaces and registers it under the
// "buzz" key. The interpreter core has no dependency on magus; this package is
// the only seam between the two, translating engine.Value ↔ buzz.Value.
//
// magus's own buzz execution path (interp.runtime) drives the concrete
// *buzz.Session directly for Exec/Targets/CallVal, which the engine.Session
// interface doesn't expose; this adapter exists for generic engine.Session
// consumers (e.g. the cross-engine benchmark and any tooling that enumerates
// the engine registry).
package buzz

import (
	"context"
	"fmt"

	core "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/egladman/magus/internal/interp/engine"
)

func init() {
	engine.Register("buzz", engineImpl{})
}

type engineImpl struct{}

func (engineImpl) ID() string { return "buzz" }

func (engineImpl) NewSession(ctx context.Context) (engine.Session, error) {
	return &session{core: core.NewSession(ctx, core.WithEmbedded()), ctx: ctx}, nil
}

// session wraps a *core.Session and satisfies engine.Session. ctx is the
// session's bound context (from NewSession); Call honors it so the documented
// cancellation contract holds on the generic engine path.
type session struct {
	core *core.Session
	ctx  context.Context
}

func (s *session) Close() error { return s.core.Close() }

func (s *session) SetGlobal(name string, v engine.Value) { s.core.SetGlobal(name, fromEngine(v)) }

func (s *session) GetGlobal(name string) engine.Value { return toEngine(s.core.GetGlobal(name)) }

func (s *session) NewTable() engine.Table { return &table{} }

// LoadString compiles code against the session's shared-globals scope and
// returns the compiled Chunk as an engine.Value. Execution is deferred to Call,
// so the same chunk can be run multiple times without recompiling.
func (s *session) LoadString(code string) (engine.Value, error) {
	chunk, err := s.core.Compile(code)
	if err != nil {
		return nil, err
	}
	return codeValue{s: s, chunk: chunk}, nil
}

func (s *session) DoString(code string) error { return s.core.DoString(code) }

func (s *session) Call(p engine.CallParams) error {
	v, ok := p.Fn.(codeValue)
	if !ok {
		return fmt.Errorf("buzz: Call: value is not a compiled Buzz chunk")
	}
	ctx := v.s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return v.s.core.ExecChunk(ctx, v.chunk)
}

func toEngine(v vm.Value) engine.Value {
	switch {
	case v.IsNull():
		return engine.NilValue
	case v.IsBool():
		return engine.BoolValue(v.AsBool())
	case v.IsInt():
		return engine.NumberValue(float64(v.AsInt()))
	case v.IsFloat():
		return engine.NumberValue(v.AsFloat())
	case v.IsStr():
		return engine.StringValue(v.AsString())
	case v.IsList():
		return &table{items: v.ListItems()}
	case v.IsMap(), v.IsObject():
		mv, _ := v.MapView()
		return &table{mapV: mv}
	default:
		return value{v}
	}
}

func fromEngine(ev engine.Value) vm.Value {
	if ev == nil || ev.IsNil() {
		return vm.Null
	}
	if v, ok := ev.(value); ok {
		return v.v
	}
	if s, ok := ev.AsString(); ok {
		return vm.StrValue(s)
	}
	if n, ok := ev.AsNumber(); ok {
		return vm.FloatValue(n)
	}
	if t, ok := ev.AsTable(); ok {
		m := vm.NewMap()
		t.ForEach(func(k, val engine.Value) {
			if key, ok := k.AsString(); ok {
				m.MapSet(key, fromEngine(val))
			}
		})
		return m
	}
	return vm.BoolValue(ev.AsBool())
}

// codeValue wraps a compiled Buzz Chunk as an engine.Value.
type codeValue struct {
	s     *session
	chunk *vm.Chunk
}

func (codeValue) IsNil() bool                        { return false }
func (codeValue) AsBool() bool                       { return true }
func (codeValue) String() string                     { return "<buzz:code>" }
func (codeValue) AsString() (string, bool)           { return "", false }
func (codeValue) AsNumber() (float64, bool)          { return 0, false }
func (codeValue) AsTable() (engine.Table, bool)      { return nil, false }
func (c codeValue) AsFunction() (engine.Value, bool) { return c, true }

// value wraps a non-collection vm.Value (or a function) as engine.Value.
type value struct{ v vm.Value }

func (e value) IsNil() bool    { return e.v.IsNull() }
func (e value) String() string { return e.v.String() }
func (e value) AsString() (string, bool) {
	if e.v.IsStr() {
		return e.v.AsString(), true
	}
	return "", false
}

func (e value) AsNumber() (float64, bool) {
	switch {
	case e.v.IsInt():
		return float64(e.v.AsInt()), true
	case e.v.IsFloat():
		return e.v.AsFloat(), true
	}
	return 0, false
}
func (e value) AsBool() bool { return e.v.Bool() }
func (e value) AsTable() (engine.Table, bool) {
	if e.v.IsList() {
		return &table{items: e.v.ListItems()}, true
	}
	if mv, ok := e.v.MapView(); ok {
		return &table{mapV: mv}, true
	}
	return nil, false
}

func (e value) AsFunction() (engine.Value, bool) {
	if e.v.IsFun() || e.v.IsDirect() {
		return e, true
	}
	return nil, false
}

// table adapts a Buzz map and/or list to engine.Table. String keys use the map
// half; integer keys use the items slice, growing as needed.
type table struct {
	mapV  vm.Value   // map-backed entries
	items []vm.Value // list-backed entries (1-indexed via RawSetInt/RawGetInt)
}

func (t *table) IsNil() bool                      { return false }
func (t *table) AsBool() bool                     { return true }
func (t *table) AsString() (string, bool)         { return "", false }
func (t *table) AsNumber() (float64, bool)        { return 0, false }
func (t *table) AsFunction() (engine.Value, bool) { return nil, false }
func (t *table) AsTable() (engine.Table, bool)    { return t, true }

func (t *table) String() string {
	if len(t.items) > 0 {
		return vm.ListValue(t.items).String()
	}
	if t.mapV.IsMap() {
		return t.mapV.String()
	}
	return "{}"
}

func (t *table) RawSetString(key string, v engine.Value) {
	if !t.mapV.IsMap() {
		t.mapV = vm.NewMap()
	}
	t.mapV.MapSet(key, fromEngine(v))
}

func (t *table) RawGetString(key string) engine.Value {
	if v, ok := t.mapV.MapGet(key); ok {
		return toEngine(v)
	}
	return engine.NilValue
}

func (t *table) RawSetInt(key int, v engine.Value) {
	idx := key - 1
	for len(t.items) <= idx {
		t.items = append(t.items, vm.Null)
	}
	t.items[idx] = fromEngine(v)
}

func (t *table) RawGetInt(key int) engine.Value {
	idx := key - 1
	if idx < 0 || idx >= len(t.items) {
		return engine.NilValue
	}
	return toEngine(t.items[idx])
}

func (t *table) ForEach(fn func(k, v engine.Value)) {
	if t.mapV.IsMap() {
		for _, k := range t.mapV.MapKeys() {
			v, _ := t.mapV.MapGet(k)
			fn(engine.StringValue(k), toEngine(v))
		}
	}
	for i, item := range t.items {
		fn(engine.NumberValue(float64(i+1)), toEngine(item))
	}
}

func (t *table) Len() int {
	if len(t.items) > 0 {
		return len(t.items)
	}
	if t.mapV.IsMap() {
		return len(t.mapV.MapKeys())
	}
	return 0
}
