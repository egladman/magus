package vm

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/egladman/gopherbuzz/ast"
)

// Callable is a Go function invocable as a Buzz value.
//
// No-retain contract: args is a view into the VM's operand stack, valid only
// for the duration of the call. A Callable must not retain args (or any
// sub-slice of it) past return — copy out anything it needs to keep. Reading
// args and copying out individual Values (which are values, not aliases) is
// fine; holding the slice header is not. This lets OpCall pass the stack window
// directly instead of allocating a fresh slice per direct call (see vm.go).
type Callable func(ctx context.Context, args []Value) (Value, error)

// valueTag discriminates the kind of a Value.
//
// ultra-opt: a uint8 tag avoids the two-word interface dispatch of the old Val
// interface. Immediate kinds (null/bool/int/float) carry their payload in num
// and set obj=nil — zero GC allocation. Heap kinds carry one GC-visible pointer
// in obj, allocated exactly once at value creation.
//
// The concrete representation of the obj field (a typed interface vs a raw
// unsafe.Pointer) is build-tag-selected: see value_unsafe.go (default) and
// value_safe.go (-tags buzz_safe). Everything in this file is representation-
// agnostic: heap Values are built with heapValue(tag, ptr) and read back with
// the asX accessors, both of which the two representation files implement.
// measured: BenchmarkLoopSum allocs/op: 4 000 000 → ~0 (run BenchmarkLoopSum to reproduce).
// assumes: amd64/arm64, Go 1.22+, GC sees all heap roots through obj.
type valueTag uint8

const (
	tagNull      valueTag = iota
	tagBool               // num: 0=false, 1=true
	tagInt                // num: int64 bits
	tagFloat              // num: float64 bits (math.Float64bits)
	tagStr                // obj: *strObj
	tagList               // obj: *listObj
	tagMap                // obj: *mapObj
	tagFun                // obj: *funObj
	tagDirect             // obj: *directObj
	tagObject             // obj: *objectInst
	tagObjectDef          // obj: *objectDefObj
	tagEnumDef            // obj: *enumDefObj
	tagEnumVal            // obj: *enumValObj
	tagIterState          // obj: *iterStateObj
	tagObjDecl            // obj: *objDeclPayload (wraps *ast.ObjectDecl)
	tagRange              // obj: *rangeObj
	tagFib                // obj: *fibObj
)

// Value is defined in value_unsafe.go / value_safe.go (build-tag-selected),
// along with the heapVal interface, the heapValue constructor, the asX
// accessors, and the obj-equality helper sameObj. All three are the only
// representation-dependent pieces; the rest of this file is shared.

// --- heap payload types ---

type strObj struct{ V string }
type listObj struct{ Items []Value }

// mapObj backs Buzz map literals. Keys/Vals/keyVals are parallel slices in
// insertion order; M is a lazily-built key→index hash that exists only once
// the map outgrows smallMapThreshold.
//
// ultra-opt: most maps and (especially) object field sets are tiny — a handful
//
//	of keys — so the Go map's construction alloc and per-access hash are pure
//	overhead. Below smallMapThreshold, set/get linear-scan Keys (a few string
//	compares over a contiguous slice, branch-predictor- and cache-friendly) and
//	M stays nil, so constructing a small map/object allocates no map at all. M
//	is built once on the set that crosses the threshold, after which all lookups
//	go through it again in O(1).
//	measured: see bench/smallmap.txt (BenchmarkFieldAccess, BenchmarkForeachMap).
//	trade-off: a get/set on a near-threshold map is O(n) string compares instead
//	  of O(1); n<=smallMapThreshold bounds it, and the scan beats hashing for
//	  these sizes in practice.
//	assumes: Keys/Vals/keyVals stay index-parallel (maintained by set); M, when
//	  non-nil, maps every existing key to its slice index.
type mapObj struct {
	Keys    []string
	Vals    []Value          // parallel to Keys; indexed value access (no map lookup for iteration)
	keyVals []Value          // pre-built StrValue per key; zero-alloc map key iteration
	M       map[string]int32 // key → index in Keys/Vals/keyVals; nil until size > smallMapThreshold
}

// smallMapThreshold is the key count at or below which a mapObj skips its Go map
// and linear-scans Keys instead. Tuned against BenchmarkFieldAccess /
// BenchmarkForeachMap; object field sets are almost always well under it.
const smallMapThreshold = 8

type funObj struct {
	Params []string
	Chunk  *Chunk
	Env    *Env    // definition-time env for globals/closures
	Upvals []Value // captured upvalues (snapshot at closure creation)
	This   Value   // non-null for bound methods; zero Value = unbound
}
type directObj struct {
	Name string
	Fn   Callable
}
type objectInst struct {
	Def    *objectDefObj
	Fields []Value // declaration-order flat field values; indexed by def.Fields[i]
}

// methodEntry is one slot in an object type's method table (vtable). Methods are
// stored in declaration order as a small ordered slice rather than a Go map: a
// type has a handful of methods, so a linear scan over contiguous names is faster
// than a string hash (no aeshash, no map probe) — the same trade-off mapObj makes
// for small field sets. The ordering also gives each method a stable index, the
// foundation for index-based (guard-free) dispatch on a statically-typed receiver.
type methodEntry struct {
	Name string
	Fn   *funObj
}

type objectDefObj struct {
	Name    string
	Fields  []ast.ObjField
	Methods []methodEntry
	Env     *Env
}

// fieldIndex returns the declaration-order index of field name, or -1 if absent.
func (d *objectDefObj) fieldIndex(name string) int {
	for i := range d.Fields {
		if d.Fields[i].Name == name {
			return i
		}
	}
	return -1
}

// method resolves a method by name via linear scan, returning it and ok=true, or
// (nil, false) if the type has no such method.
func (d *objectDefObj) method(name string) (*funObj, bool) {
	for i := range d.Methods {
		if d.Methods[i].Name == name {
			return d.Methods[i].Fn, true
		}
	}
	return nil, false
}

// objDeclPayload wraps *ast.ObjectDecl so it satisfies heapVal without
// requiring the ast package to import the buzz value representation.
type objDeclPayload struct{ *ast.ObjectDecl }

func (o *objDeclPayload) heapKind() valueTag { return tagObjDecl }

type enumDefObj struct {
	Name  string
	Cases []string
}
type enumValObj struct {
	Enum string
	Case string
}
type rangeObj struct{ Lo, Hi int64 }

type fibStatus uint8

const (
	fibSuspended fibStatus = iota
	fibRunning
	fibDone
)

// fibObj is a suspended fiber: an independent VM snapshot resumable via
// resume(). Each fiber owns its own VM (stack + frames), so distinct fibers
// never share execution state.
//
// Debug introspection: while the fiber is running (inside resume()), the
// session's curVM points at the fiber's VM, so Frames()/CallDepth()/step hooks
// reflect the fiber's call stack. This is achieved by session.builtinResume
// calling session.enter(f.vm) before exec() — the same save-and-restore used
// for every other run path.
//
// Threading model: a fiber, like the Session that created it, is owned by one
// goroutine at a time. Buzz has no goroutine primitive, so a fiber value cannot
// escape to another goroutine from script code, and the host pool drives each
// Session on a single goroutine. The status field therefore guards only the
// reachable misuse — recursive resume() from within the running fiber on the
// same goroutine — and needs no lock. Fibers in different Sessions run on
// different goroutines safely because the Sessions share no mutable state.
type fibObj struct {
	vm        *VM
	status    fibStatus
	returnVal Value // cached return value set when the fiber completes (for resolve)
	err       error // cached terminal error when the fiber's VM failed; re-surfaced by a later resume/resolve instead of being swallowed
}

type iterStateObj struct {
	list     *listObj
	mapObj   *mapObj
	rng      *rangeObj
	rangeIdx int64
	idx      int
}

func (*strObj) heapKind() valueTag       { return tagStr }
func (*listObj) heapKind() valueTag      { return tagList }
func (*mapObj) heapKind() valueTag       { return tagMap }
func (*funObj) heapKind() valueTag       { return tagFun }
func (*directObj) heapKind() valueTag    { return tagDirect }
func (*objectInst) heapKind() valueTag   { return tagObject }
func (*objectDefObj) heapKind() valueTag { return tagObjectDef }
func (*enumDefObj) heapKind() valueTag   { return tagEnumDef }
func (*enumValObj) heapKind() valueTag   { return tagEnumVal }
func (*iterStateObj) heapKind() valueTag { return tagIterState }
func (*rangeObj) heapKind() valueTag     { return tagRange }
func (*fibObj) heapKind() valueTag       { return tagFib }

// --- constructors ---
//
// Null/True/False and the immediate constructors (IntValue/FloatValue/BoolValue)
// live in the representation files (value_unsafe.go / value_safe.go) because they
// set the concrete encoding. The heap constructors below go through heapValue and
// are representation-agnostic.

// strIntern maps string content → *strObj so equal strings share one pointer.
// The pointer-equality fast path in valuesEqual then resolves string equality
// without a content scan for any string that was created through StrValue.
var strIntern sync.Map // string → *strObj

func internStr(s string) *strObj {
	if v, ok := strIntern.Load(s); ok {
		return v.(*strObj)
	}
	obj := &strObj{V: s}
	if actual, loaded := strIntern.LoadOrStore(s, obj); loaded {
		return actual.(*strObj)
	}
	return obj
}

// StrValue returns a Buzz string Value wrapping s. Interns the *strObj so that
// equal strings always share the same pointer, enabling O(1) equality via
// pointer comparison in valuesEqual.
func StrValue(s string) Value { return heapValue(tagStr, internStr(s)) }

// ListValue returns a Buzz list Value backed by items. items may be nil.
func ListValue(items []Value) Value {
	return heapValue(tagList, &listObj{Items: items})
}

// DirectValue wraps a Go Callable as a Buzz function value bound to name.
func DirectValue(name string, fn Callable) Value {
	return heapValue(tagDirect, &directObj{Name: name, Fn: fn})
}

// rangeValue constructs a range [lo, hi]. Package-internal; use the .. operator in Buzz.
func rangeValue(lo, hi int64) Value {
	return heapValue(tagRange, &rangeObj{Lo: lo, Hi: hi})
}

// ObjDeclValue wraps *ast.ObjectDecl as a Buzz value (used by the compiler).
func ObjDeclValue(decl *ast.ObjectDecl) Value {
	return heapValue(tagObjDecl, &objDeclPayload{decl})
}

// EnumDefValue creates a Buzz enum-definition value (used by the compiler).
func EnumDefValue(name string, cases []string) Value {
	return heapValue(tagEnumDef, &enumDefObj{Name: name, Cases: cases})
}

// NullValue returns the Buzz null value (convenience alias for Null).
func NullValue() Value { return Null }

// --- exported scalar accessors ---

// AsInt returns the int64 payload. Only valid when IsInt() is true.
func (v Value) AsInt() int64 { return int64(v.num()) }

// AsFloat returns the float64 payload. Only valid when IsFloat() is true.
func (v Value) AsFloat() float64 { return math.Float64frombits(v.num()) }

// AsBool returns the bool payload. Only valid when IsBool() is true.
func (v Value) AsBool() bool { return v.num() != 0 }

// --- exported type predicates ---

// IsNull reports whether v is null.
func (v Value) IsNull() bool { return v.tag() == tagNull }

// IsBool reports whether v is a boolean.
func (v Value) IsBool() bool { return v.tag() == tagBool }

// IsInt reports whether v is an integer.
func (v Value) IsInt() bool { return v.tag() == tagInt }

// IsFloat reports whether v is a float.
func (v Value) IsFloat() bool { return v.tag() == tagFloat }

// IsStr reports whether v is a string.
func (v Value) IsStr() bool { return v.tag() == tagStr }

// IsList reports whether v is a list.
func (v Value) IsList() bool { return v.tag() == tagList }

// IsMap reports whether v is a map.
func (v Value) IsMap() bool { return v.tag() == tagMap }

// IsFun reports whether v is a function (Buzz-defined or direct Go callable).
func (v Value) IsFun() bool { return v.tag() == tagFun || v.tag() == tagDirect }

// IsDirect reports whether v is a direct Go callable (host function).
func (v Value) IsDirect() bool { return v.tag() == tagDirect }

// IsObject reports whether v is an object instance.
func (v Value) IsObject() bool { return v.tag() == tagObject }

// Kind returns the Buzz type name for this value (e.g. "int", "str", "null").
func (v Value) Kind() string { return v.buzzKind() }

// buzzKind returns the Buzz type name for error messages.
func (v Value) buzzKind() string {
	switch v.tag() {
	case tagNull:
		return "null"
	case tagBool:
		return "bool"
	case tagInt:
		return "int"
	case tagFloat:
		return "float"
	case tagStr:
		return "str"
	case tagList:
		return "list"
	case tagMap:
		return "map"
	case tagFun:
		return "fun"
	case tagDirect:
		return "direct"
	case tagObject:
		return "object"
	case tagObjectDef:
		return "objectdef"
	case tagEnumDef:
		return "enumdef"
	case tagEnumVal:
		return "enum"
	case tagIterState:
		return "iterstate"
	case tagRange:
		return "rng"
	case tagFib:
		return "fib"
	default:
		return "unknown"
	}
}

// String returns the Buzz string representation of v.
func (v Value) String() string {
	switch v.tag() {
	case tagNull:
		return "null"
	case tagBool:
		if v.num() != 0 {
			return "true"
		}
		return "false"
	case tagInt:
		return strconv.FormatInt(int64(v.num()), 10)
	case tagFloat:
		return strconv.FormatFloat(math.Float64frombits(v.num()), 'g', -1, 64)
	case tagStr:
		return v.asStr().V
	case tagList:
		l := v.asList()
		var sb strings.Builder
		sb.WriteByte('[')
		for i, item := range l.Items {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(item.String())
		}
		sb.WriteByte(']')
		return sb.String()
	case tagMap:
		m := v.asMap()
		var sb strings.Builder
		sb.WriteByte('{')
		for i, k := range m.Keys {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(strconv.Quote(k))
			sb.WriteString(": ")
			sb.WriteString(m.Vals[i].String())
		}
		sb.WriteByte('}')
		return sb.String()
	case tagFun:
		return "<fun>"
	case tagDirect:
		return fmt.Sprintf("<direct:%s>", v.asDirect().Name)
	case tagObject:
		inst := v.asObject()
		var sb strings.Builder
		sb.WriteString(inst.Def.Name)
		sb.WriteByte('{')
		for i, df := range inst.Def.Fields {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(strconv.Quote(df.Name))
			sb.WriteString(": ")
			sb.WriteString(inst.Fields[i].String())
		}
		sb.WriteByte('}')
		return sb.String()
	case tagObjectDef:
		return fmt.Sprintf("<object %s>", v.asObjectDef().Name)
	case tagEnumDef:
		return fmt.Sprintf("<enum %s>", v.asEnumDef().Name)
	case tagEnumVal:
		ev := v.asEnumVal()
		return ev.Enum + "." + ev.Case
	case tagRange:
		r := v.asRange()
		return fmt.Sprintf("%d..%d", r.Lo, r.Hi)
	case tagFib:
		f := v.asFib()
		switch f.status {
		case fibSuspended:
			return "<fib:suspended>"
		case fibRunning:
			return "<fib:running>"
		default:
			return "<fib:done>"
		}
	default:
		return "<unknown>"
	}
}

// Bool returns the truthiness of v. Only null and false are falsy.
func (v Value) Bool() bool {
	switch v.tag() {
	case tagNull:
		return false
	case tagBool:
		return v.num() != 0
	default:
		return true
	}
}

// --- mapObj helpers ---

// newMapObj returns an empty mapObj. M is left nil: small maps linear-scan
// (see mapObj's ultra-opt note) and the hash is built lazily on growth.
func newMapObj() *mapObj { return &mapObj{} }

// indexOf returns the slice index of key, or -1 if absent. It uses M when built
// (large maps) and otherwise linear-scans Keys (small maps).
func (m *mapObj) indexOf(key string) int {
	if m.M != nil {
		if n, ok := m.M[key]; ok {
			return int(n)
		}
		return -1
	}
	for i, k := range m.Keys {
		if k == key {
			return i
		}
	}
	return -1
}

func (m *mapObj) set(key string, v Value) {
	if i := m.indexOf(key); i >= 0 {
		m.Vals[i] = v
		return
	}
	idx := int32(len(m.Keys))
	m.Keys = append(m.Keys, key)
	m.keyVals = append(m.keyVals, StrValue(key))
	m.Vals = append(m.Vals, v)
	if m.M != nil {
		m.M[key] = idx
	} else if len(m.Keys) > smallMapThreshold {
		// Crossed the threshold: build the hash once, then maintain it.
		m.M = make(map[string]int32, len(m.Keys))
		for i, k := range m.Keys {
			m.M[k] = int32(i)
		}
	}
}

func (m *mapObj) get(key string) (Value, bool) {
	if i := m.indexOf(key); i >= 0 {
		return m.Vals[i], true
	}
	return Null, false
}

// --- equality ---

// valuesEqual reports structural equality for scalars and strings, and
// reference equality for collections (lists, maps, objects). Two distinct
// list or map values are never equal even if their contents match — this is
// an intentional language design choice that avoids O(n) comparison costs.
func valuesEqual(a, b Value) bool {
	if a.tag() != b.tag() {
		// int == float cross-type
		if a.tag() == tagInt && b.tag() == tagFloat {
			return float64(int64(a.num())) == math.Float64frombits(b.num())
		}
		if a.tag() == tagFloat && b.tag() == tagInt {
			return math.Float64frombits(a.num()) == float64(int64(b.num()))
		}
		return false
	}
	switch a.tag() {
	case tagNull:
		return true
	case tagBool, tagInt:
		return a.num() == b.num()
	case tagFloat:
		return math.Float64frombits(a.num()) == math.Float64frombits(b.num())
	case tagStr:
		ap, bp := a.asStr(), b.asStr()
		return ap == bp || ap.V == bp.V // pointer fast path (interned), then content
	case tagEnumVal:
		ae, be := a.asEnumVal(), b.asEnumVal()
		return ae.Enum == be.Enum && ae.Case == be.Case
	default:
		return sameObj(a, b) // reference equality for collections/objects
	}
}

// --- embedding API ---
//
// The functions and methods below let host code build and inspect Buzz values
// without reaching into unexported representation. Mutation must go through
// MapSet so the map's key-iteration cache stays consistent.

// RawEqual reports whether two scalar values have identical tag and numeric
// representation. For heap values (str, list, map, fun, object, fib) it
// compares pointer identity, not structural equality. Intended for tests that
// need to assert two values produced by different execution paths are
// "the same scalar kind and payload".
func (v Value) RawEqual(other Value) bool {
	return v.tag() == other.tag() && v.num() == other.num()
}

// FunName returns the declared name of a function value: the name of a Buzz
// `fun` declaration or a Go DirectValue, or "" for a non-function or an
// anonymous closure. It lets host code recover which exported handler a function
// value refers to, rather than trusting a parallel string key.
func (v Value) FunName() string {
	switch v.tag() {
	case tagFun:
		if f := v.asFun(); f != nil && f.Chunk != nil {
			return f.Chunk.Name
		}
	case tagDirect:
		if d := v.asDirect(); d != nil {
			return d.Name
		}
	}
	return ""
}

// FunDoc returns the documentation comment of a Buzz `fun` value — the comment
// block immediately preceding its declaration — or "" for a non-function, an
// anonymous closure, a Go DirectValue, or a function recovered from bytecode
// (Doc is not serialized; see Chunk.Doc). It lets host code recover a spell
// target handler's comment, the companion to FunName.
func (v Value) FunDoc() string {
	if v.tag() == tagFun {
		if f := v.asFun(); f != nil && f.Chunk != nil {
			return f.Chunk.Doc
		}
	}
	return ""
}

// AsString returns the string payload. Only valid when IsStr() is true.
// Named AsString (not AsStr) to match the cross-engine engine.Value accessor
// convention shared with the gopherlua/luajit/js backends.
func (v Value) AsString() string { return v.asStr().V }

// ListItems returns the list items slice. Only valid when IsList() is true.
func (v Value) ListItems() []Value { return v.asList().Items }

// MapKeys returns the ordered key slice. Only valid when IsMap() is true.
func (v Value) MapKeys() []string { return v.asMap().Keys }

// MapView returns the underlying map Value for maps and object instances.
// For maps it returns self. For object instances it returns a Value wrapping
// the fields map. Returns (Null, false) for all other types.
func (v Value) MapView() (Value, bool) {
	switch v.tag() {
	case tagMap:
		return v, true
	case tagObject:
		inst := v.asObject()
		m := newMapObj()
		for i, df := range inst.Def.Fields {
			m.set(df.Name, inst.Fields[i])
		}
		return heapValue(tagMap, m), true
	}
	return Null, false
}

// NewMap returns an empty Buzz map Value.
func NewMap() Value { return heapValue(tagMap, newMapObj()) }

// MapSet stores key→val on a map Value. No-op if v is not a map.
func (v Value) MapSet(key string, val Value) {
	if v.tag() == tagMap {
		v.asMap().set(key, val)
	}
}

// MapGet returns the value at key and whether it was present. Returns
// (Null, false) if v is not a map or the key is absent.
func (v Value) MapGet(key string) (Value, bool) {
	if v.tag() == tagMap {
		return v.asMap().get(key)
	}
	return Null, false
}
