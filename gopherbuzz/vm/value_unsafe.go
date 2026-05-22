//go:build buzz_unsafe

package vm

import (
	"math"
	"unsafe"

	"github.com/egladman/gopherbuzz/ast"
)

// This file is the default (fast) representation of a Buzz Value's heap payload.
// The safe counterpart in value_safe.go (built with -tags buzz_safe) uses a
// plain interface and is behaviourally identical; CI builds and tests both so
// this unsafe form is always checked against the safe one.
//
// ── Why unsafe.Pointer here ──────────────────────────────────────────────────
//
// A Value is a tagged union: an 8-bit tag plus an 8-byte immediate payload
// (num) for null/bool/int/float, or one pointer to a heap object for the
// reference kinds (str/list/map/fun/object/…). The obvious Go encoding stores
// that pointer as an interface (heapVal), which is what value_safe.go does. But
// an interface is *two* words — a type pointer (itab) and a data pointer — so it
// makes Value 32 bytes, and every read of the payload (asStr, asList, …) pays a
// runtime type assertion that compares the itab.
//
// We already carry a discriminant: the tag. It tells us the concrete type of
// the payload with total certainty (the tag and the pointer are set together,
// in lockstep, by heapValue and never independently mutated). So the interface's
// type word is pure redundancy on the hot path. Storing the payload as a raw
// unsafe.Pointer drops the type word entirely:
//
//   - Value shrinks from 32 to 24 bytes. The operand stack and every frame's
//     `this` field are Values, so this is less memory to copy on every push,
//     pop, replaceTop2, and call, and more Values per cache line.
//   - asStr/asList/… become a single pointer cast — `(*strObj)(v.obj)` — with no
//     itab compare. The compiler turns the cast into a no-op; the load is just
//     the field read.
//
// ── Why this is safe (i.e. why it does not break the GC or alias rules) ──────
//
// unsafe.Pointer is dangerous in general because the compiler and garbage
// collector stop reasoning about what it points at. Two properties make this
// particular use sound, and both are enforced structurally rather than by
// convention:
//
//  1. GC visibility. The Go GC scans unsafe.Pointer fields exactly as it scans
//     typed pointers — an unsafe.Pointer is a real, traced pointer (unlike
//     uintptr, which is just an integer the GC ignores). heapValue stores the
//     result of converting a live *T (e.g. *strObj) with unsafe.Pointer(p), and
//     that pointer keeps the pointee reachable for as long as the Value is
//     reachable. We never stash a pointer as uintptr, never do pointer
//     arithmetic on obj, and never synthesize an obj that doesn't come from a
//     real Go allocation, so the heap object's lifetime is tracked correctly
//     and it is never collected out from under a live Value. (This is the one
//     blessed unsafe.Pointer conversion pattern: *T ⇄ unsafe.Pointer.)
//
//  2. Type correctness. The tag is the single source of truth for obj's dynamic
//     type, and heapValue is the *only* way to populate obj. Every constructor
//     sets the tag and the matching pointer together, and the asX accessors cast
//     back to the type the tag promises. A tag/pointer mismatch would therefore
//     require a VM bug in this package (a wrong tag passed to heapValue, or an
//     asX called on the wrong tag), not anything reachable from Buzz source.
//     The safe build (value_safe.go) keeps the checked interface assertion that
//     *panics loudly* on exactly that mismatch, so running the test +
//     conformance suite under `-tags buzz_safe` will catch any such bug — the
//     fast build is validated by its slow twin rather than by faith.
//
// In short: the tag already encodes the type, the GC already traces
// unsafe.Pointer, and the safe build re-checks the invariant — so dropping the
// interface's redundant type word is a pure win with no soundness cost.
//
// measured: see bench/value_unsafe.txt.
// assumes: every heap kind's obj is produced by heapValue from a real *T; the
//   tag always matches that *T; obj is never uintptr-laundered or offset.

// Value is the Buzz runtime value. Immediates (null/bool/int/float) live in num
// and leave obj nil; heap kinds carry one GC-traced pointer in obj, with tag as
// the discriminant. 24 bytes (vs 32 for the interface form in value_safe.go).
//
// tag/num are unexported fields read through the tag()/num() accessor methods so
// the shared VM code (which calls v.tag()/v.num()) is representation-agnostic: an
// alternate rep — e.g. a NaN-boxed uint64 — can compute them from bits instead of
// storing fields, without touching a single call site. The accessors are trivial
// field reads the compiler inlines to nothing, so there is no cost here.
type Value struct {
	t   valueTag
	n   uint64         // immediate payload (int64/float64 bits, bool)
	obj unsafe.Pointer // nil for immediates; *strObj/*listObj/… for heap kinds
}

func (v Value) tag() valueTag { return v.t }
func (v Value) num() uint64   { return v.n }

// Immediate constructors live in the representation file because they set the
// concrete encoding. The heap constructors (StrValue, ListValue, …) go through
// heapValue and stay shared in value.go.
var (
	Null  = Value{t: tagNull}
	True  = Value{t: tagBool, n: 1}
	False = Value{t: tagBool, n: 0}
)

// IntValue returns a Buzz integer Value wrapping n.
func IntValue(n int64) Value { return Value{t: tagInt, n: uint64(n)} }

// FloatValue returns a Buzz float Value wrapping f.
func FloatValue(f float64) Value { return Value{t: tagFloat, n: math.Float64bits(f)} }

// BoolValue returns True or False.
func BoolValue(b bool) Value {
	if b {
		return True
	}
	return False
}

// heapVal constrains heapValue's type parameter to the set of payload pointer
// types. It carries no methods — heapKind() is retained on the payload types
// only so the safe build can share them — but pinning the constraint here keeps
// heapValue callers honest: you can only build a heap Value from a real payload
// pointer, never from an arbitrary unsafe.Pointer.
type heapVal interface {
	*strObj | *listObj | *mapObj | *funObj | *directObj | *objectInst |
		*objectDefObj | *enumDefObj | *enumValObj | *iterStateObj | *rangeObj |
		*fibObj | *objDeclPayload
}

// heapValue builds a heap Value: it pairs tag with ptr, converting the typed
// pointer to the GC-traced unsafe.Pointer obj field. This is the single place a
// Value's obj is populated, so the tag↔type pairing is established here once and
// the asX accessors trust it (see the file header for why that is sound).
//
// ptr is read out as an unsafe.Pointer via the *(*unsafe.Pointer)(&ptr) reinterpret
// idiom: ptr is itself one pointer word, so &ptr is its address and reading that
// word as an unsafe.Pointer yields the heap address with the GC's pointer bit
// intact (no uintptr laundering, which would hide the pointer from the GC). A
// plain unsafe.Pointer(ptr) conversion is not permitted on a union-constrained
// type parameter, hence the reinterpret.
func heapValue[T heapVal](tag valueTag, ptr T) Value {
	//nolint:gosec // G103: audited — reinterprets a live *T as unsafe.Pointer (the
	// blessed *T<->unsafe.Pointer pattern); GC-traced, no uintptr laundering. See
	// the file header for the full GC-safety argument; buzz_safe uses the interface.
	return Value{t: tag, obj: *(*unsafe.Pointer)(unsafe.Pointer(&ptr))}
}

// sameObj reports pointer identity of two heap Values' payloads (used for
// reference equality of lists/maps/objects). Comparing the raw pointers is
// exactly the interface-identity comparison the safe build does.
func sameObj(a, b Value) bool { return a.obj == b.obj }

// ptrAs casts obj back to *T. tag must match (see file header); the cast is a
// no-op the compiler erases, so the asX wrappers below are free.
func ptrAs[T any](v Value) *T { return (*T)(v.obj) }

func (v Value) asStr() *strObj             { return ptrAs[strObj](v) }
func (v Value) asList() *listObj           { return ptrAs[listObj](v) }
func (v Value) asMap() *mapObj             { return ptrAs[mapObj](v) }
func (v Value) asFun() *funObj             { return ptrAs[funObj](v) }
func (v Value) asDirect() *directObj       { return ptrAs[directObj](v) }
func (v Value) asObject() *objectInst      { return ptrAs[objectInst](v) }
func (v Value) asObjectDef() *objectDefObj { return ptrAs[objectDefObj](v) }
func (v Value) asEnumDef() *enumDefObj     { return ptrAs[enumDefObj](v) }
func (v Value) asEnumVal() *enumValObj     { return ptrAs[enumValObj](v) }
func (v Value) asIterState() *iterStateObj { return ptrAs[iterStateObj](v) }
func (v Value) asRange() *rangeObj         { return ptrAs[rangeObj](v) }
func (v Value) asFib() *fibObj             { return ptrAs[fibObj](v) }

// asObjDecl returns the *ast.ObjectDecl payload. Only valid when tag == tagObjDecl.
func (v Value) asObjDecl() *ast.ObjectDecl { return ptrAs[objDeclPayload](v).ObjectDecl }

// VM-context accessors — zero-cost wrappers in M2 (vm is ignored; the pointer
// cast is identical to the value-local form). In M3 (buzz_nanbox) they resolve
// the per-VM heap-table index rather than casting a raw pointer.
func (vm *VM) asStr(v Value) *strObj             { return v.asStr() }
func (vm *VM) asList(v Value) *listObj           { return v.asList() }
func (vm *VM) asMap(v Value) *mapObj             { return v.asMap() }
func (vm *VM) asFun(v Value) *funObj             { return v.asFun() }
func (vm *VM) asDirect(v Value) *directObj       { return v.asDirect() }
func (vm *VM) asObject(v Value) *objectInst      { return v.asObject() }
func (vm *VM) asObjectDef(v Value) *objectDefObj { return v.asObjectDef() }
func (vm *VM) asEnumDef(v Value) *enumDefObj     { return v.asEnumDef() }
func (vm *VM) asEnumVal(v Value) *enumValObj     { return v.asEnumVal() }
func (vm *VM) asIterState(v Value) *iterStateObj { return v.asIterState() }
func (vm *VM) asRange(v Value) *rangeObj         { return v.asRange() }
func (vm *VM) asFib(v Value) *fibObj             { return v.asFib() }
func (vm *VM) asObjDecl(v Value) *ast.ObjectDecl { return v.asObjDecl() }

// VM-context allocators — zero-cost wrappers in M2 (delegate to package-level
// heapValue). In M3 (buzz_nanbox) they intern the object into the per-VM heap
// table and return an index-backed Value.
func (vm *VM) allocFun(ptr *funObj) Value             { return heapValue(tagFun, ptr) }
func (vm *VM) allocMap(ptr *mapObj) Value             { return heapValue(tagMap, ptr) }
func (vm *VM) allocFib(ptr *fibObj) Value             { return heapValue(tagFib, ptr) }
func (vm *VM) allocObject(ptr *objectInst) Value      { return heapValue(tagObject, ptr) }
func (vm *VM) allocObjectDef(ptr *objectDefObj) Value { return heapValue(tagObjectDef, ptr) }
func (vm *VM) allocIterState(ptr *iterStateObj) Value { return heapValue(tagIterState, ptr) }
func (vm *VM) allocEnumVal(ptr *enumValObj) Value     { return heapValue(tagEnumVal, ptr) }
