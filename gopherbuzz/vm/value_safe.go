//go:build buzz_safe

package vm

import (
	"math"

	"github.com/egladman/gopherbuzz/ast"
)

// This file is the safe (interface-based) representation of a Buzz Value's heap
// payload, selected with `-tags buzz_safe`. It is behaviourally identical to the
// default unsafe.Pointer form in value_unsafe.go but stores the payload as a
// typed interface and re-checks the tag↔type invariant with a panicking type
// assertion (objAs).
//
// Its purpose is verification, not speed: CI builds and tests the package under
// this tag as well as the default, so any place the fast build's tag and pointer
// could disagree (a wrong tag passed to heapValue, or an asX called on the wrong
// tag) trips the loud panic here under the test + conformance suite. The fast
// build is thereby validated by its slow twin. Keep the two files in lockstep —
// every accessor in one has a counterpart in the other.
//
// This is the original pre-Phase-3 representation, retained verbatim so the
// comparison is apples-to-apples.

// Value is the Buzz runtime value. Immediates carry no heap pointer; heap kinds
// carry one GC-visible typed pointer in obj (two words: itab + data). 32 bytes.
// tag/num are read through the tag()/num() accessors — see value_unsafe.go for
// why the shared VM code goes through them.
type Value struct {
	t   valueTag
	n   uint64  // immediate payload
	obj heapVal // nil for null/bool/int/float; typed for heap kinds
}

func (v Value) tag() valueTag { return v.t }
func (v Value) num() uint64   { return v.n }

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

// heapVal is the interface implemented by all heap-allocated value payloads.
// heapKind() exists so this build can recover (or assert) the kind; the unsafe
// build uses the same payload types but a union constraint instead.
type heapVal interface{ heapKind() valueTag }

// heapValue builds a heap Value, storing ptr as the interface obj. Mirrors the
// unsafe build's heapValue so call sites are identical across both.
func heapValue[T heapVal](tag valueTag, ptr T) Value {
	return Value{t: tag, obj: ptr}
}

// sameObj reports payload identity (reference equality for lists/maps/objects).
// Interface comparison compares (type, data); since the tags already matched at
// the call site, this is equivalent to the unsafe build's raw pointer compare.
func sameObj(a, b Value) bool { return a.obj == b.obj }

// objAs panics on tag/obj mismatch. The discriminant tag is set in lockstep with
// obj at construction, so a mismatch is an internal invariant violation, not a
// user error — fail loud rather than return (T, bool). This is the checked
// boundary the unsafe build elides; running under -tags buzz_safe turns a
// tag/pointer mismatch into a visible panic instead of a silent bad cast.
func objAs[T heapVal](v Value, kind string) T {
	o, ok := v.obj.(T)
	if !ok {
		panic("buzz: internal: Value tag/obj mismatch: expected " + kind + ", got " + v.buzzKind())
	}
	return o
}

func (v Value) asStr() *strObj             { return objAs[*strObj](v, "str") }
func (v Value) asUD() *udObj               { return objAs[*udObj](v, "ud") }
func (v Value) asList() *listObj           { return objAs[*listObj](v, "list") }
func (v Value) asMap() *mapObj             { return objAs[*mapObj](v, "map") }
func (v Value) asFun() *funObj             { return objAs[*funObj](v, "fun") }
func (v Value) asDirect() *directObj       { return objAs[*directObj](v, "direct") }
func (v Value) asObject() *objectInst      { return objAs[*objectInst](v, "object") }
func (v Value) asObjectDef() *objectDefObj { return objAs[*objectDefObj](v, "objectdef") }
func (v Value) asEnumDef() *enumDefObj     { return objAs[*enumDefObj](v, "enumdef") }
func (v Value) asEnumVal() *enumValObj     { return objAs[*enumValObj](v, "enumval") }
func (v Value) asIterState() *iterStateObj { return objAs[*iterStateObj](v, "iter") }
func (v Value) asRange() *rangeObj         { return objAs[*rangeObj](v, "range") }
func (v Value) asFib() *fibObj             { return objAs[*fibObj](v, "fib") }
func (v Value) asPat() *patObj             { return objAs[*patObj](v, "pat") }

// asObjDecl returns the *ast.ObjectDecl payload. Only valid when tag == tagObjDecl.
func (v Value) asObjDecl() *ast.ObjectDecl { return objAs[*objDeclPayload](v, "objectdecl").ObjectDecl }

// VM-context accessors — zero-cost wrappers in M2 (vm is ignored; the type
// assertion is identical to the value-local form). In M3 (buzz_nanbox) they
// resolve the per-VM heap-table index instead.
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

// VM-context allocators — zero-cost wrappers in M2 (delegate to package-level
// heapValue). In M3 (buzz_nanbox) they intern into the per-VM heap table.
func (vm *VM) allocFun(ptr *funObj) Value             { return heapValue(tagFun, ptr) }
func (vm *VM) allocMap(ptr *mapObj) Value             { return heapValue(tagMap, ptr) }
func (vm *VM) allocFib(ptr *fibObj) Value             { return heapValue(tagFib, ptr) }
func (vm *VM) allocObject(ptr *objectInst) Value      { return heapValue(tagObject, ptr) }
func (vm *VM) allocObjectDef(ptr *objectDefObj) Value { return heapValue(tagObjectDef, ptr) }
func (vm *VM) allocIterState(ptr *iterStateObj) Value { return heapValue(tagIterState, ptr) }
func (vm *VM) allocEnumVal(ptr *enumValObj) Value     { return heapValue(tagEnumVal, ptr) }
