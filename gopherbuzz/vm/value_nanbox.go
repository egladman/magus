//go:build !buzz_safe && !buzz_unsafe

package vm

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/egladman/gopherbuzz/ast"
)

// NaN-box encoding: all Buzz values packed into one uint64 word.
//
// A 64-bit IEEE 754 double is a quiet NaN when bits 62-51 are all 1 (mask
// 0x7FF8_0000_0000_0000, named qnanMask). Any uint64 with those bits set is
// not a valid normal float; we use that space for non-float Buzz types.
// Normal doubles (including ±Inf) pass through as raw bits; incoming NaNs
// are canonicalised to a reserved quiet-NaN pattern (nanboxNaN).
//
// Layout for tagged (non-float) values:
//   bits 62-51 : qnanMask bits (always set for tagged values)
//   bits 50-48 : coarse kind (3 bits)
//   bits 47- 0 : payload (48 bits)
//     for heap refs: bits 47-44 = fine heap kind (4 bits), bits 43-0 = table index (44 bits)
//     for int/bool/null: full 48 bits used as the immediate payload
//
// Coarse kinds:
//   0 nanboxNull — null             (payload unused)
//   1 nanboxBool — bool             (payload: 0=false, 1=true)
//   2 nanboxInt  — int48            (payload: 48-bit signed integer)
//   3 nanboxHeap — heap ref         (bits 47-44: fine kind → valueTag; bits 43-0: gHeapArr index)
//   4 nanboxNaN  — canonical NaN float (num() returns a standard quiet-NaN bit pattern)
//   5-7          — reserved
//
// Heap fine kinds offset from tagStr (4): fine 0=tagStr, 1=tagList, …, 12=tagFib.
//
// int48 range: ±2^47 ≈ ±140 trillion — sufficient for magus scripting.
//
// GC anchoring: all heap objects live in the global grow-only slice gHeapArr.
// The stack holds uint64 indices → no GC write barriers on stack push/pop/arith.
// Objects are pinned for the process lifetime (M3 trade-off; M4 can compact).

const (
	qnanMask    = uint64(0x7FF8_0000_0000_0000)
	payMask48   = uint64(0x0000_FFFF_FFFF_FFFF) // bits 47-0: 48-bit payload
	idxMask44   = uint64(0x0000_0FFF_FFFF_FFFF) // bits 43-0: 44-bit heap index
	coarseShift = 48
	fineShift   = 44

	nanboxNull = uint64(0)
	nanboxBool = uint64(1)
	nanboxInt  = uint64(2)
	nanboxHeap = uint64(3)
	nanboxNaN  = uint64(4)

	// canonNaNBits is returned by num() for a NaN float value.
	// math.Float64frombits(canonNaNBits) is a quiet NaN.
	canonNaNBits = uint64(0x7FF8_0000_0000_0001)
)

// Value is the NaN-boxed Buzz runtime value (8 bytes).
// Immediates (null/bool/int/float) are self-contained.
// Heap refs carry an index into the global gHeapArr table.
type Value uint64

func (v Value) tag() valueTag {
	u := uint64(v)
	if u&qnanMask != qnanMask {
		return tagFloat
	}
	switch (u >> coarseShift) & 7 {
	case nanboxNull:
		return tagNull
	case nanboxBool:
		return tagBool
	case nanboxInt:
		return tagInt
	case nanboxHeap:
		fine := (u >> fineShift) & 0xF
		return valueTag(fine + uint64(tagStr))
	default: // nanboxNaN and reserved → float
		return tagFloat
	}
}

func (v Value) num() uint64 {
	u := uint64(v)
	if u&qnanMask != qnanMask {
		return u // normal float: bits are the float64 payload
	}
	switch (u >> coarseShift) & 7 {
	case nanboxNaN:
		return canonNaNBits
	case nanboxInt:
		p := u & payMask48
		if p&(1<<47) != 0 { // sign-extend from bit 47
			return p | ^payMask48
		}
		return p
	default:
		return u & payMask48
	}
}

var (
	Null  = Value(qnanMask | (nanboxNull << coarseShift))
	True  = Value(qnanMask | (nanboxBool << coarseShift) | 1)
	False = Value(qnanMask | (nanboxBool << coarseShift))
)

// IntValue returns a Buzz integer Value. Inputs outside ±2^47 are silently truncated.
func IntValue(n int64) Value {
	return Value(qnanMask | (nanboxInt << coarseShift) | (uint64(n) & payMask48))
}

// FloatValue returns a Buzz float Value, canonicalising NaN inputs.
func FloatValue(f float64) Value {
	if math.IsNaN(f) {
		return Value(qnanMask | (nanboxNaN << coarseShift))
	}
	return Value(math.Float64bits(f))
}

// BoolValue returns True or False.
func BoolValue(b bool) Value {
	if b {
		return True
	}
	return False
}

// heapVal is the interface satisfied by all heap payload types.
// Mirrors value_safe.go so both safe and nanbox builds use interface-based storage.
type heapVal interface{ heapKind() valueTag }

// ─── Global heap table ────────────────────────────────────────────────────────
//
// All heap objects live in the global table anchored by gHeapPtr. Heap Values
// carry a 44-bit index into this slice; the stack is therefore pointerless
// ([]uint64) and requires no GC write barriers on push/pop/arithmetic.
//
// Reads are lock-free: gHeapGet loads the *[]heapVal atomic pointer and indexes
// the slice directly (no mutex). Writes hold gHeapMu to serialise concurrent
// appends, then atomically publish the new slice header via gHeapPtr.Store.
//
// Safety: an index only becomes reachable in a Value after gHeapAlloc returns,
// which is after the atomic store. Any goroutine that can observe the index in a
// Value must have received it through a happens-before channel that already
// includes the store, so the load in gHeapGet always sees a slice long enough to
// contain the index.
//
// Objects are never removed (pinned for the process lifetime). Acceptable for
// short-lived sessions; M5 can add safe-point compaction.

var (
	gHeapMu  sync.Mutex
	gHeapPtr atomic.Pointer[[]heapVal]
)

func init() {
	s := make([]heapVal, 0, 256)
	gHeapPtr.Store(&s)
}

func gHeapAlloc(ptr heapVal) uint64 {
	gHeapMu.Lock()
	s := *gHeapPtr.Load()
	idx := uint64(len(s))
	s = append(s, ptr)
	gHeapPtr.Store(&s)
	gHeapMu.Unlock()
	return idx
}

// gHeapGet returns the heap object at idx. Lock-free: loads an atomic snapshot
// of the slice header and indexes it directly.
func gHeapGet(idx uint64) heapVal {
	return (*gHeapPtr.Load())[idx]
}

// encodeHeap packs a valueTag and global heap index into a NaN-box heap-ref Value.
func encodeHeap(t valueTag, idx uint64) Value {
	fine := uint64(t - tagStr) // tagStr (4) is the base of heap tags; fine kind 0=str, ...
	return Value(qnanMask | (nanboxHeap << coarseShift) | (fine << fineShift) | (idx & idxMask44))
}

// heapValue interns ptr into the global heap table and returns the NaN-box Value.
// This is the shared constructor used by StrValue, ListValue, NewMap, etc.
func heapValue[T heapVal](tag valueTag, ptr T) Value {
	return encodeHeap(tag, gHeapAlloc(ptr))
}

// internedStrValue returns the string Value for an interned *strObj, caching its
// global-heap index on the object (stored as index+1, 0 = unset) so every Value
// for the same content shares one heap entry instead of appending a fresh one on
// each StrValue call. This keeps string-churning loops (e.g. a sliding sub())
// from growing the never-freed heap. A lost CAS race orphans one heap slot,
// which is bounded and harmless since it still points at the same strObj.
func internedStrValue(o *strObj) Value {
	if e := atomic.LoadUint64(&o.heapIdx); e != 0 {
		return encodeHeap(tagStr, e-1)
	}
	idx := gHeapAlloc(o)
	if !atomic.CompareAndSwapUint64(&o.heapIdx, 0, idx+1) {
		idx = atomic.LoadUint64(&o.heapIdx) - 1
	}
	return encodeHeap(tagStr, idx)
}

// sameObj reports heap identity. Callers have already verified a.tag() == b.tag(),
// so the fine-kind bits match; only the index varies between distinct objects.
func sameObj(a, b Value) bool {
	return uint64(a)&payMask48 == uint64(b)&payMask48
}

// value-level asX accessors: look up the global heap table.

func nanboxObj(v Value) heapVal            { return gHeapGet(uint64(v) & idxMask44) }
func (v Value) asStr() *strObj             { return nanboxObj(v).(*strObj) }
func (v Value) asUD() *udObj               { return nanboxObj(v).(*udObj) }
func (v Value) asList() *listObj           { return nanboxObj(v).(*listObj) }
func (v Value) asMap() *mapObj             { return nanboxObj(v).(*mapObj) }
func (v Value) asFun() *funObj             { return nanboxObj(v).(*funObj) }
func (v Value) asDirect() *directObj       { return nanboxObj(v).(*directObj) }
func (v Value) asObject() *objectInst      { return nanboxObj(v).(*objectInst) }
func (v Value) asObjectDef() *objectDefObj { return nanboxObj(v).(*objectDefObj) }
func (v Value) asEnumDef() *enumDefObj     { return nanboxObj(v).(*enumDefObj) }
func (v Value) asEnumVal() *enumValObj     { return nanboxObj(v).(*enumValObj) }
func (v Value) asIterState() *iterStateObj { return nanboxObj(v).(*iterStateObj) }
func (v Value) asRange() *rangeObj         { return nanboxObj(v).(*rangeObj) }
func (v Value) asFib() *fibObj             { return nanboxObj(v).(*fibObj) }
func (v Value) asPat() *patObj             { return nanboxObj(v).(*patObj) }

// asObjDecl returns the *ast.ObjectDecl payload. Only valid when tag == tagObjDecl.
func (v Value) asObjDecl() *ast.ObjectDecl { return nanboxObj(v).(*objDeclPayload).ObjectDecl }

// VM-context accessors: delegate to the value-level forms (global heap).
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
func (vm *VM) asPat(v Value) *patObj             { return v.asPat() }
func (vm *VM) asObjDecl(v Value) *ast.ObjectDecl { return v.asObjDecl() }

// VM-context allocators: intern into the global heap.
func (vm *VM) allocFun(ptr *funObj) Value             { return heapValue(tagFun, ptr) }
func (vm *VM) allocMap(ptr *mapObj) Value             { return heapValue(tagMap, ptr) }
func (vm *VM) allocFib(ptr *fibObj) Value             { return heapValue(tagFib, ptr) }
func (vm *VM) allocObject(ptr *objectInst) Value      { return heapValue(tagObject, ptr) }
func (vm *VM) allocObjectDef(ptr *objectDefObj) Value { return heapValue(tagObjectDef, ptr) }
func (vm *VM) allocIterState(ptr *iterStateObj) Value { return heapValue(tagIterState, ptr) }
func (vm *VM) allocEnumVal(ptr *enumValObj) Value     { return heapValue(tagEnumVal, ptr) }
