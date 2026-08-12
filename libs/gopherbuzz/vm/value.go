package vm

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
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
// optimization: a uint8 tag avoids the two-word interface dispatch of the old Val
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
	tagPat                // obj: *patObj
	tagUD                 // obj: *udObj (foreign FFI pointer; heap-boxed to carry the full 64-bit address)
	tagType               // obj: *typeObj (a type used as a value: `<[str]>`, `typeof x`)
	tagCell               // obj: *cellObj (the shared box behind a captured local)
)

// Value is defined in value_unsafe.go / value_safe.go (build-tag-selected),
// along with the heapVal interface, the heapValue constructor, the asX
// accessors, and the obj-equality helper sameObj. All three are the only
// representation-dependent pieces; the rest of this file is shared.

// --- heap payload types ---

// strObj backs Buzz strings. V is the immutable content. ascii caches whether V
// is pure ASCII (0 unknown, 1 yes, -1 no), computed once by isASCII; when true,
// a rune index equals a byte index, letting rune-indexed methods (sub) slice
// bytes directly instead of materializing a []rune of the whole string per call.
//
// heapIdx is used only by the NaN-box build (internedStrValue): it caches this
// interned string's global-heap index (stored as index+1, 0 = unset) so that
// every Value for the same content shares one heap entry instead of appending a
// fresh one on each StrValue call. The safe/unsafe builds store the *strObj
// pointer in the Value directly and never read it.
type strObj struct {
	V       string
	heapIdx uint64
	ascii   int32
	// runeCur memoizes one (rune index, byte offset) pair for the multibyte path of
	// rune-indexed access: high 32 bits the rune index, low 32 the byte offset, packed
	// into one word so an atomic load can never tear the pair apart and hand out a byte
	// offset belonging to a different rune index. Zero means unset, which is also the
	// correct answer for rune 0, so no sentinel is needed.
	runeCur atomic.Uint64
}

// runeCursor returns the memoized (rune index, byte offset) hint, or (0, 0).
func (o *strObj) runeCursor() (runeIdx, byteOff int) {
	packed := o.runeCur.Load()
	return int(packed >> 32), int(packed & 0xffffffff)
}

// setRuneCursor memoizes a known (rune index, byte offset) pair. Racing callers
// derive genuine pairs for the same string, so whichever wins is still correct - the
// cursor is a hint that changes how far scanRune walks, never what it returns.
// Offsets past 4 GiB are not memoized rather than truncated to a wrong value.
func (o *strObj) setRuneCursor(runeIdx, byteOff int) {
	// Widened to uint64 before comparing, rather than testing against an untyped
	// 0xffffffff: `int` is 32 bits on linux/armv6, linux/armv7 and 386, where that
	// constant overflows it and this package does not compile AT ALL. Both operands are
	// already known non-negative, so the conversion is total. On a 32-bit host the bound
	// is simply unreachable, which is the correct reading - a >4 GiB offset cannot arise
	// there in the first place.
	if runeIdx < 0 || byteOff < 0 || uint64(runeIdx) > math.MaxUint32 || uint64(byteOff) > math.MaxUint32 {
		return
	}
	o.runeCur.Store(uint64(runeIdx)<<32 | uint64(byteOff))
}

// scanRune returns the rune at rune index `at` and its byte offset, or ok=false when
// `at` is past the end.
//
// This exists because rune-indexed access into a multibyte string used to convert the
// WHOLE string with []rune on every call, which made any character-at-a-time walk
// quadratic and allocated a fresh rune slice per character. The access pattern that
// matters is sequential (i = 0, 1, 2, ...), so remembering where the last lookup landed
// turns the walk into single-rune steps. A backwards or distant jump just restarts from
// the beginning, which is no worse than the conversion it replaces and allocates nothing
// either way.
func (o *strObj) scanRune(at int) (r rune, byteOff int, ok bool) {
	if at < 0 {
		return 0, 0, false
	}
	ri, bi := o.runeCursor()
	if at < ri {
		ri, bi = 0, 0 // asked for an earlier rune: the hint cannot help, rescan
	}
	for bi < len(o.V) {
		dr, size := utf8.DecodeRuneInString(o.V[bi:])
		if ri == at {
			o.setRuneCursor(ri, bi)
			return dr, bi, true
		}
		bi += size
		ri++
	}
	return 0, 0, false
}

// isASCII reports whether V is pure ASCII, caching the answer. Concurrent callers
// derive the same value, so the store is idempotent and needs no lock.
func (o *strObj) isASCII() bool {
	switch atomic.LoadInt32(&o.ascii) {
	case 1:
		return true
	case -1:
		return false
	}
	v := int32(1)
	for i := 0; i < len(o.V); i++ {
		if o.V[i] >= utf8.RuneSelf {
			v = -1
			break
		}
	}
	atomic.StoreInt32(&o.ascii, v)
	return v == 1
}

// udObj boxes a foreign FFI pointer (`ud`). It is heap-allocated because a full
// 64-bit address fits neither the NaN-boxed int's ~48-bit payload nor a float64
// mantissa (tagged pointers like CFNumber set high bits AND carry a low-bit tag)
// — only an out-of-line word preserves every bit losslessly.
type udObj struct{ Addr uintptr }

// listObj backs Buzz list literals. Mut reports whether the list may be mutated
// in place (append, subscript-set, …); plain `[…]` literals are immutable, only
// `mut [...]` sets Mut.
type listObj struct {
	Items []Value
	Mut   bool
}

// mapObj backs Buzz map literals. Keys/Vals/keyVals are parallel slices in
// insertion order; M is a lazily-built key→index hash that exists only once
// the map outgrows smallMapThreshold.
//
// optimization: most maps are tiny — a handful of keys — so the Go map's
//
//	construction alloc and per-access hash are pure overhead. Below
//	smallMapThreshold, set/get linear-scan Keys (a few string
//	compares over a contiguous slice, branch-predictor- and cache-friendly) and
//	M stays nil, so constructing a small map allocates no map at all. M
//	is built once on the set that crosses the threshold, after which all lookups
//	go through it again in O(1).
//	measured: see bench/smallmap.txt (BenchmarkFieldAccess, BenchmarkForeachMap).
//	note: this comment used to say object FIELD SETS were the main beneficiary.
//	  They are not, and have not been since named objects moved to objectInst's
//	  flat declaration-order Fields slice — BenchmarkFieldAccess does not touch
//	  mapObj at all. The stale claim made this struct look far hotter than it is
//	  and inflated the estimated cost of the object-key work above.
//	trade-off: a get/set on a near-threshold map is O(n) string compares instead
//	  of O(1); n<=smallMapThreshold bounds it, and the scan beats hashing for
//	  these sizes in practice.
//	assumes: Keys/Vals/keyVals stay index-parallel (maintained by set); M, when
//	  non-nil, maps every existing key to its slice index.
//
// keyVals is the AUTHORITATIVE key: upstream keys a map by any value
// (`std.AutoArrayHashMapUnmanaged(Value, Value)`), so `{ bandit: true }` holds
// an object. Keys is that key's display string and stays the identity only
// while every key is a str — the shape that covers records, anonymous object
// literals and every host-built map, and the one the hash and the linear scan
// above are tuned for. objKeyed says the map has left that shape.
type mapObj struct {
	Keys    []string
	Vals    []Value          // parallel to Keys; indexed value access (no map lookup for iteration)
	keyVals []Value          // parallel to Keys; the real key value, and what foreach pushes
	M       map[string]int32 // key → index in Keys/Vals/keyVals; nil until size > smallMapThreshold
	Mut     bool             // mutable only for `mut {…}` literals (immutable by default)
	// objKeyed reports that at least one key is not a str, so Keys is no longer a
	// faithful identity ("1" the str and 1 the int share a display string) and
	// every lookup has to scan keyVals with mapKeyEqual instead.
	//
	// trade-off: such a map gets NO hash at any size - M is dropped on the set
	//   that promotes it and never rebuilt - so lookups are O(n) rather than the
	//   O(1) a str-keyed map regains above smallMapThreshold. Keying M would need
	//   a synthetic per-key identity string, which costs an allocation on every
	//   get of every map to serve a shape neither this embedding nor upstream's
	//   suite builds at size. Revisit if a large non-str-keyed map ever shows up
	//   in a profile.
	objKeyed bool
}

// smallMapThreshold is the key count at or below which a mapObj skips its Go map
// and linear-scans Keys instead. Tuned against BenchmarkFieldAccess /
// BenchmarkForeachMap; object field sets are almost always well under it.
const smallMapThreshold = 8

type funObj struct {
	Params []string
	Chunk  *Chunk
	Env    *Env    // definition-time env for globals/closures
	Upvals []Value // captured cells; each entry is a *cellObj shared with the defining frame
	This   Value   // non-null for bound methods; zero Value = unbound
}
type directObj struct {
	Name string
	Fn   Callable
}
type objectInst struct {
	Def    *objectDefObj
	Fields []Value // declaration-order flat field values; indexed by def.Fields[i]
	Mut    bool    // mutable only for `mut Foo{…}` instances (immutable by default)
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
	// Statics are `static fun` methods, dispatched on the type value (Foo.make())
	// rather than an instance, so they are kept out of the instance vtable above.
	Statics []methodEntry
	// StaticFields is the type's own mutable state: one value per `static` field,
	// read and written through the type value (Foo.next). It makes a def mutable,
	// which is sound because buildObjectDef allocates a fresh def per execution of
	// the declaration rather than sharing one across VMs.
	StaticFields []staticFieldEntry
	Env          *Env
}

// staticFieldEntry is one slot of a type's static state. Like methodEntry it is
// an ordered slice scanned linearly: a type declares a handful of statics, so a
// name compare over contiguous entries beats a map probe.
type staticFieldEntry struct {
	Name string
	Val  Value
}

// staticFieldIndex returns the index of static field name, or -1 if absent.
func (d *objectDefObj) staticFieldIndex(name string) int {
	for i := range d.StaticFields {
		if d.StaticFields[i].Name == name {
			return i
		}
	}
	return -1
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

// staticMethod resolves a `static fun` by name via linear scan, returning it and
// ok=true, or (nil, false) if the type has no such static method.
func (d *objectDefObj) staticMethod(name string) (*funObj, bool) {
	for i := range d.Statics {
		if d.Statics[i].Name == name {
			return d.Statics[i].Fn, true
		}
	}
	return nil, false
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
	// vals interns one Value per case, built on first use and shared thereafter.
	//
	// optimization: an enum value is immutable and fully determined by (def, case
	//   index), so `Kind.one` was allocating an identical enumValObj on every
	//   evaluation - and under the nanbox representation each allocation also takes
	//   the global heap mutex and appends a permanent entry to the global heap table.
	//   A four-arm `match` over an enum paid that up to four times per iteration.
	// measured: BenchmarkMatchEnum 175007 -> 50007 allocs/op; the alloc profile
	//   attributed 71% of the benchmark's objects to getMember -> allocEnumVal.
	// trade-off: one Value slice retained per enum definition, for the life of the
	//   definition. Enums are declared, not constructed, so the count is bounded by
	//   the source.
	// assumes: enum values are compared STRUCTURALLY (valuesEqual, tagEnumVal case),
	//   so sharing a pointer can only make values equal that already were.
	vals []Value
	// Values holds each case's `.value`, parallel to Cases: the ordinal for a
	// plain enum, the case name for an `enum<str>`, or whatever literal the case
	// assigned. The compiler resolves all three forms, so the VM only reads.
	Values []Value
}
type enumValObj struct {
	Enum string
	Case string
	// Val is the case's `.value`. Stored at construction because the value carries
	// only names, and recovering it later would mean finding the definition again.
	Val Value
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
	list *listObj
	// enumDef holds an enum being iterated; its cases become enum VALUES, so the
	// loop variable behaves the same as one written Enum.case.
	enumDef *enumDefObj
	// strBytes holds a string being iterated, as its raw BYTES. Upstream iterates a
	// string bytewise (its str builtins index bytes throughout), and decoding runes
	// here made iteration LOSSY for a string holding arbitrary binary: every invalid
	// UTF-8 byte came back as U+FFFD, so a buffer written with a double could not be
	// read back a byte at a time. Concatenating the elements still reproduces the
	// original string, which is what upstream's foreach.buzz checks.
	strBytes []byte
	mapObj   *mapObj
	rng      *rangeObj
	fib      *fibObj
	rangeIdx int64
	idx      int
}

func (*strObj) heapKind() valueTag    { return tagStr }
func (*listObj) heapKind() valueTag   { return tagList }
func (*mapObj) heapKind() valueTag    { return tagMap }
func (*funObj) heapKind() valueTag    { return tagFun }
func (*directObj) heapKind() valueTag { return tagDirect }

// cellObj is the shared box behind a local that a closure captured. Upstream Buzz
// closures capture by REFERENCE: a closure assigning to an enclosing local updates
// the one variable, not a private copy. gopherbuzz used to copy the Value into the
// closure's Upvals, which silently answered wrong (`sum` stayed 0 after `add(5)`).
// A captured slot now holds a cell for the whole life of its frame, and every
// access -- from the owning frame and from the closure -- goes through it, so there
// is a single storage location and no open/close upvalue bookkeeping.
type cellObj struct{ v Value }

func (*cellObj) heapKind() valueTag { return tagCell }

func (*objectInst) heapKind() valueTag   { return tagObject }
func (*objectDefObj) heapKind() valueTag { return tagObjectDef }
func (*enumDefObj) heapKind() valueTag   { return tagEnumDef }
func (*enumValObj) heapKind() valueTag   { return tagEnumVal }
func (*iterStateObj) heapKind() valueTag { return tagIterState }
func (*rangeObj) heapKind() valueTag     { return tagRange }
func (*fibObj) heapKind() valueTag       { return tagFib }
func (*udObj) heapKind() valueTag        { return tagUD }

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
// pointer comparison in valuesEqual. internedStrValue is build-specific: the
// NaN-box build caches one global-heap index per interned string (so repeated
// content does not churn the heap), while the safe/unsafe builds carry the
// pointer in the Value directly.
func StrValue(s string) Value { return internedStrValue(internStr(s)) }

// UDValue boxes a foreign FFI pointer (`ud`). See udObj for why it is heap-boxed.
func UDValue(addr uintptr) Value { return heapValue(tagUD, &udObj{Addr: addr}) }

// ListValue returns a Buzz list Value backed by items. items may be nil.
func ListValue(items []Value) Value {
	return heapValue(tagList, &listObj{Items: items})
}

// listValue is ListValue with an explicit mutability, for the built-in methods
// that return a list of the RECEIVER's own type (sub, filter, reverse, and the
// clone family). Upstream types those from `obj_list` itself, so a `mut [int]`
// keeps its mutability across them; building the result immutable made
// `list.cloneMutable().sub(0)` reject the very mutation upstream permits.
func listValue(items []Value, mut bool) Value {
	return heapValue(tagList, &listObj{Items: items, Mut: mut})
}

// DirectValue wraps a Go Callable as a Buzz function value bound to name.
func DirectValue(name string, fn Callable) Value {
	return heapValue(tagDirect, newDirect(name, fn))
}

// newDirect builds the callable a builtin method dispatches to WITHOUT giving it
// a Value. On the nanbox build every Value takes a slot in an append-only object
// heap that is never reclaimed, so wrapping a bound method per property access
// pinned one object per call: `xs.append(j)` in a loop leaked a slot an
// iteration. OpInvoke calls this closure in place; only a property access that is
// not immediately called (`final f = xs.append;`) still needs a Value, which
// getMember builds. See builtinMethod.
func newDirect(name string, fn Callable) *directObj {
	return &directObj{Name: name, Fn: fn}
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
// values holds each case's resolved `.value`, parallel to cases; pass nil for a
// plain enum, whose cases take their ordinals.
func EnumDefValue(name string, cases []string, values []Value) Value {
	if values == nil {
		values = make([]Value, len(cases))
		for i := range cases {
			values[i] = IntValue(int64(i))
		}
	}
	return heapValue(tagEnumDef, &enumDefObj{Name: name, Cases: cases, Values: values})
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

// IsUD reports whether v is a foreign FFI pointer (`ud`).
func (v Value) IsUD() bool { return v.tag() == tagUD }

// AsUD returns the boxed foreign pointer. Only valid when IsUD() is true.
func (v Value) AsUD() uintptr { return v.asUD().Addr }

// IsList reports whether v is a list.
func (v Value) IsList() bool { return v.tag() == tagList }

// IsMap reports whether v is a map.
func (v Value) IsMap() bool { return v.tag() == tagMap }

// IsFun reports whether v is a function (Buzz-defined or direct Go callable).
func (v Value) IsFun() bool { return v.tag() == tagFun || v.tag() == tagDirect }

// IsDirect reports whether v is a direct Go callable (host function).
func (v Value) IsDirect() bool { return v.tag() == tagDirect }

// IsObjectDef reports whether v is an object TYPE (the thing `object Foo {}` binds),
// as opposed to an instance of one. Exported for the session's aliased-import path,
// which has to tell a module's type declarations apart from its ordinary values.
func (v Value) IsObjectDef() bool { return v.tag() == tagObjectDef }

// IsObject reports whether v is an object instance.
func (v Value) IsObject() bool { return v.tag() == tagObject }

// ObjectTypeName returns the declared type name of an object DEF or an INSTANCE,
// and "" for anything else. A host module needs it to recognize a foreign struct
// passed as a type argument (`ffi\sizeOfStruct(Data)`) or as a value.
func (v Value) ObjectTypeName() string {
	switch v.tag() {
	case tagObjectDef:
		return v.asObjectDef().Name
	case tagObject:
		if d := v.asObject().Def; d != nil {
			return d.Name
		}
	}
	return ""
}

// ObjectFieldAt returns an instance's i-th field in DECLARATION order, and
// ok=false when v is not an instance or i is past its fields. Declaration order is
// what a host module needs: it is the order a foreign struct's layout is computed in.
func (v Value) ObjectFieldAt(i int) (Value, bool) {
	if v.tag() != tagObject {
		return Null, false
	}
	inst := v.asObject()
	if i < 0 || i >= len(inst.Fields) {
		return Null, false
	}
	return inst.Fields[i], true
}

// NewInstance builds an instance of the object TYPE v, taking fields in
// declaration order. It errors when v is not a type, so a host module cannot
// silently produce something shaped like an object but belonging to nothing.
func (v Value) NewInstance(fields []Value) (Value, error) {
	if v.tag() != tagObjectDef {
		return Null, fmt.Errorf("buzz: cannot instantiate %s: not an object type", v.buzzKind())
	}
	def := v.asObjectDef()
	vals := make([]Value, len(def.Fields))
	copy(vals, fields)
	return heapValue(tagObject, &objectInst{Def: def, Fields: vals, Mut: true}), nil
}

// ForeignStructTypes returns the C field type spellings of a zdef-declared struct
// or union, and ok=false when the name is not one. It is how a host module reads a
// foreign layout without importing the FFI provider's internals.
func ForeignStructTypes(name string) ([]string, bool) {
	t, ok := declaredFieldTypes[name]
	return t, ok
}

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
		return "double"
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
	case tagPat:
		return "pat"
	case tagUD:
		return "ud"
	case tagType:
		return "type"
	default:
		return "unknown"
	}
}

// String returns the Buzz string representation of v.
func (v Value) String() string {
	return v.stringPath(nil)
}

// stringPath is String's recursive workhorse. path is every list/map/object
// currently being rendered on this call stack, tracked by heap identity
// (sameObj), not content. Buzz lists and maps are heap objects mutable in
// place (list.append et al.), so `[any] l = mut []; l.append(l);` is a real
// reference cycle: naive recursion here would stack-overflow, which in Go is a
// FATAL, unrecoverable error, not something a recover() can paper over. String
// backs str(), print, and string interpolation — upstream-visible surface — so
// unlike an internal safety check, this must render something rather than
// error: a revisited collection prints a placeholder and recursion stops there.
func (v Value) stringPath(path []Value) string {
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
		if pathHasIdentity(path, v) {
			return "[...]"
		}
		path = append(path, v)
		l := v.asList()
		var sb strings.Builder
		sb.WriteByte('[')
		for i, item := range l.Items {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(item.stringPath(path))
		}
		sb.WriteByte(']')
		return sb.String()
	case tagMap:
		if pathHasIdentity(path, v) {
			return "{...}"
		}
		path = append(path, v)
		m := v.asMap()
		var sb strings.Builder
		sb.WriteByte('{')
		for i, k := range m.Keys {
			if i > 0 {
				sb.WriteString(", ")
			}
			// Quoted for a str key, bare otherwise: `{1: "a"}` reads back as source,
			// where `{"1": "a"}` would name a different map (see mapKeyEqual).
			if m.keyVals[i].tag() == tagStr {
				sb.WriteString(strconv.Quote(k))
			} else {
				sb.WriteString(k)
			}
			sb.WriteString(": ")
			sb.WriteString(m.Vals[i].stringPath(path))
		}
		sb.WriteByte('}')
		return sb.String()
	case tagFun:
		return "<fun>"
	case tagDirect:
		return fmt.Sprintf("<direct:%s>", v.asDirect().Name)
	case tagObject:
		inst := v.asObject()
		if pathHasIdentity(path, v) {
			return inst.Def.Name + "{...}"
		}
		path = append(path, v)
		var sb strings.Builder
		sb.WriteString(inst.Def.Name)
		sb.WriteByte('{')
		for i, df := range inst.Def.Fields {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(strconv.Quote(df.Name))
			sb.WriteString(": ")
			sb.WriteString(inst.Fields[i].stringPath(path))
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
	case tagPat:
		return v.asPat().src
	case tagUD:
		return fmt.Sprintf("ud:0x%x", v.AsUD())
	case tagType:
		// Printed as written in source, angle brackets included, so a failing
		// assertion shows `<[any]>` rather than a bare type name.
		return "<" + v.asType().name + ">"
	default:
		return "<unknown>"
	}
}

// pathHasIdentity reports whether v (a list, map, or object) is already on
// path, by heap identity rather than content — used to break a reference
// cycle while rendering. The tag check is defensive: sameObj's contract
// (see its doc comment) assumes the caller already matched tags.
func pathHasIdentity(path []Value, v Value) bool {
	for _, p := range path {
		if p.tag() == v.tag() && sameObj(p, v) {
			return true
		}
	}
	return false
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
// (see mapObj's optimization note) and the hash is built lazily on growth.
func newMapObj() *mapObj { return &mapObj{} }

// indexOf returns the slice index of the str key named by key, or -1 if absent.
// It uses M when built (large maps) and otherwise linear-scans Keys (small maps).
// indexOfVal is the general form; this one stays because a record field read
// (getMember, setMember, MapGet) already has the name as a Go string.
func (m *mapObj) indexOf(key string) int {
	if m.objKeyed {
		// Keys is only a display string here, so "1" would match the int key 1.
		// Scan keyVals for a str of the same content instead.
		for i, kv := range m.keyVals {
			if kv.tag() == tagStr && kv.asStr().V == key {
				return i
			}
		}
		return -1
	}
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

// mapKeyEqual reports whether two map keys are the same key.
//
// It is deliberately NOT valuesEqual. Upstream stores a map as
// AutoArrayHashMapUnmanaged(Value, Value) keyed on the NaN-boxed WORD, so key
// identity there is bit identity, not `eql`: `1 == 1.0` holds but `{1: x}[1.0]`
// misses, and a heap object is its own key by reference. valuesEqual is `==`,
// which coerces int to float, and a map key must not.
//
// Two kinds are matched to gopherbuzz's `==` rather than to upstream's bits.
// A str compares by CONTENT because upstream interns strings and gopherbuzz does
// not, so bit identity would make two equal literals different keys. The value
// kinds valuesEqual already treats structurally (enum case, range, pattern, type)
// keep that here: upstream's bits would separate two independently built `0..10`,
// but nothing keys a map by one, and agreeing with `==` is the answer a reader
// would predict.
func mapKeyEqual(a, b Value) bool {
	if a.tag() != b.tag() {
		return false
	}
	switch a.tag() {
	case tagStr:
		ap, bp := a.asStr(), b.asStr()
		return ap == bp || ap.V == bp.V
	case tagNull:
		return true
	case tagBool, tagInt, tagFloat:
		return a.num() == b.num()
	default:
		return valuesEqual(a, b) // enum case, range, pattern, type: structural; the rest by reference
	}
}

// indexOfVal returns the slice index of key, or -1 if absent, keying by VALUE.
// A str key on a map that has only ever held str keys takes the tuned string
// path above; anything else scans keyVals.
func (m *mapObj) indexOfVal(key Value) int {
	if !m.objKeyed {
		if key.tag() == tagStr {
			return m.indexOf(key.asStr().V)
		}
		// A non-str key cannot be present in a str-keyed map.
		return -1
	}
	for i, kv := range m.keyVals {
		if mapKeyEqual(kv, key) {
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
	m.appendEntry(key, StrValue(key), v)
}

// setVal stores key→v keying by value, the general form of set.
func (m *mapObj) setVal(key, v Value) {
	if i := m.indexOfVal(key); i >= 0 {
		m.Vals[i] = v
		return
	}
	if !m.objKeyed && key.tag() != tagStr {
		// Promote: Keys stops being an identity, so the hash built over it has to go.
		m.objKeyed = true
		m.M = nil
	}
	m.appendEntry(key.String(), key, v)
}

// getVal returns the value at key and whether it was present, keying by value.
func (m *mapObj) getVal(key Value) (Value, bool) {
	if i := m.indexOfVal(key); i >= 0 {
		return m.Vals[i], true
	}
	return Null, false
}

// appendEntry appends a new entry the caller has already established is absent,
// keeping Keys/keyVals/Vals index-parallel and M (when it exists) in step.
func (m *mapObj) appendEntry(display string, key, v Value) {
	idx := int32(len(m.Keys))
	m.Keys = append(m.Keys, display)
	m.keyVals = append(m.keyVals, key)
	m.Vals = append(m.Vals, v)
	if m.objKeyed {
		return // no hash; see mapObj's objKeyed note
	}
	if m.M != nil {
		m.M[display] = idx
	} else if len(m.Keys) > smallMapThreshold {
		// Crossed the threshold: build the hash once, then maintain it.
		m.M = make(map[string]int32, len(m.Keys))
		for i, k := range m.Keys {
			m.M[k] = int32(i)
		}
	}
}

// removeAt drops entry i, preserving insertion order and the index-parallel
// invariant. M is rebuilt rather than patched: every index after i shifts.
func (m *mapObj) removeAt(i int) Value {
	removed := m.Vals[i]
	m.Keys = append(m.Keys[:i], m.Keys[i+1:]...)
	m.keyVals = append(m.keyVals[:i], m.keyVals[i+1:]...)
	m.Vals = append(m.Vals[:i], m.Vals[i+1:]...)
	if m.M != nil {
		m.M = make(map[string]int32, len(m.Keys))
		for j, k := range m.Keys {
			m.M[k] = int32(j)
		}
	}
	return removed
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
	case tagType:
		// Structural, like tagRange below and unlike the collections: `typeof x ==
		// <[str]>` compares two independently constructed type values, so reference
		// equality would make every such comparison false. The canonical spelling is
		// the identity (see typeval.go).
		return a.asType().name == b.asType().name
	case tagRange:
		// Structural, not by reference: `0..10 == 0..10` has to hold, and a range
		// is fully described by its two operands.
		ar, br := a.asRange(), b.asRange()
		return ar.Lo == br.Lo && ar.Hi == br.Hi
	case tagPat:
		// Structural for the same reason as tagType above: upstream compares pattern
		// SOURCES, so two separately compiled `$"hello [a-z]+"` literals are equal. The
		// compiled matcher is derived from the source, so the source is the identity.
		return a.asPat().src == b.asPat().src
	case tagUD:
		return a.AsUD() == b.AsUD() // foreign pointers compare by address
	default:
		return sameObj(a, b) // reference equality for collections/objects
	}
}

// --- embedding API ---
//
// The functions and methods below let host code build and inspect Buzz values
// without reaching into unexported representation. Mutation must go through
// MapSet so the map's key-iteration cache stays consistent.

// Equal implements Buzz `==` semantics: int/float numeric coercion, string
// content equality, and reference identity for lists, maps, objects, and
// functions. It is identical across all value representations (nanbox,
// buzz_safe, buzz_unsafe), so host code and language-level equality agree
// regardless of build tag. Prefer this over RawEqual for anything that must
// match `==`.
func (v Value) Equal(other Value) bool {
	return valuesEqual(v, other)
}

// RawEqual reports whether two values have identical raw tag and num bits.
// Heap reference identity holds only in the nanbox build, which packs a heap
// index into num; under buzz_safe and buzz_unsafe num is 0 for every heap
// value (str, list, map, fun, object, fib), so any two same-tag heap values
// compare equal here. It is therefore only meaningful for scalar values
// (null, bool, int, float) - use Equal for language-level equality.
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
//
// The keys are DISPLAY strings, which is exact for the str-keyed maps host code
// builds and reads (records, host module namespaces, decoded JSON) and lossy for
// a Buzz map keyed by anything else - `{1: x}` and `{"1": x}` both report "1",
// and MapGet distinguishes them. Iterate a non-str-keyed map from Buzz instead.
func (v Value) MapKeys() []string { return v.asMap().Keys }

// EnumValue returns an enum case's backing value - what `Enum.case.value` yields -
// and whether v is an enum case at all.
//
// It exists for a HOST reading a Buzz record, not for Buzz code, which already has
// `.value`. Without it an embedder sees only the opaque case object, so a field typed
// as an enum reads as absent rather than as its string: a spell writing
// `upTo = VersionComponent.patch` would decode to nothing, silently, which is the one
// failure mode a typed enum was adopted to prevent.
func (v Value) EnumValue() (Value, bool) {
	if v.tag() != tagEnumVal {
		return Null, false
	}
	return v.asEnumVal().Val, true
}

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

// mapValue is NewMap with an explicit mutability, the map counterpart of
// listValue: the built-in methods returning a map of the RECEIVER's own type
// (filter, diff, intersect, and the clone family) have to carry it across.
func mapValue(mut bool) Value {
	m := newMapObj()
	m.Mut = mut
	return heapValue(tagMap, m)
}

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
