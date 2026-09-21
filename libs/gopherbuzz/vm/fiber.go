package vm

import "fmt"

// NewFiber builds a suspended fiber over fn, exactly as Buzz's `&fn(arg...)` does:
// same child VM, same call setup, same suspended initial status. It is the OpFiber
// case of the interpreter loop, reachable from Go.
//
// Exported because a host that drives fibers itself (see the buzz package's
// ResumeFiber/ResolveFiber) needs a fiber to drive, and every other way of getting
// one requires Buzz source to have written `&f()`. That closes the gap for a host
// that wants to step, stream or finalize a callable it was handed as a value.
//
// The receiver is the PARENT VM, whose globals and heap the fiber shares, matching
// `&f()` evaluated in that VM. A fiber built from an unrelated VM would resolve
// globals against the wrong chunk.
func (vm *VM) NewFiber(fn Value, args []Value) (Value, error) {
	if fn.tag() != tagFun {
		return Null, fmt.Errorf("buzz: a fiber requires a Buzz function, got %s", fn.buzzKind())
	}
	fibVM := newFiberVM(vm)
	if err := fibVM.Call(fn, args); err != nil {
		return Null, err
	}
	return vm.allocFib(&fibObj{vm: fibVM, status: fibSuspended}), nil
}

// Suspend builds the control-flow sentinel a HOST NATIVE returns to suspend the fiber
// it is running inside, as though the Buzz code had written `yield v` at the call.
//
// The VM already treats this as control flow rather than a fault: raiseHostError
// filters it ahead of the catch stack and the fault hook, so it propagates to whoever
// is driving the fiber. This only exposes the constructor, because yieldSignal's field
// is unexported and an embedder cannot build one.
//
// What it is FOR: a native that would otherwise block the calling body for a long time
// (waiting on child targets, on a watcher, on a remote) can instead hand control back
// to the host, which is free to release the body's session and its scheduling slot and
// resume it later. Blocking holds both for the whole wait; suspending holds neither.
//
// The native runs ONCE. The call evaluates to v, the value passed here, exactly as
// `yield v` evaluates to v; resuming continues at the instruction after the call. So a
// suspending native needs no idempotency, and v is how it answers the body: park with
// the value the body should see.
//
// Returning this from a native called OUTSIDE a fiber dismisses the value and
// continues, matching what upstream does for `yield` in a non-fiber context, so a
// native written this way stays correct when the host chooses not to drive it.
func Suspend(v Value) error { return &yieldSignal{value: v} }

// suspendedAtCall reports that err is a native's suspend signal and, if so, COMPLETES
// the interrupted call before the suspend propagates: the call evaluates to the
// suspended value, exactly as OpYield makes `yield v` evaluate to v.
//
// slot is the stack index the call's result belongs in (the callee's own slot), so this
// is the same pair of lines the success path runs, with the suspend value in place of a
// return. Resuming then continues at the NEXT instruction with a correct stack, which is
// why the native runs only once and needs no idempotency.
//
// Mirroring OpYield is the point. It pushes its result and only then suspends, so the
// VM's whole resume story is "frames and stack are left intact, continue where you
// stopped". A call that rewound and re-executed instead would be a second, different
// resume convention living in the same interpreter.
//
// Called only from the error arm of a native dispatch, so the happy path pays nothing.
func (vm *VM) suspendedAtCall(err error, slot int) bool {
	ys, ok := err.(*yieldSignal)
	if !ok {
		return false
	}
	vm.stack[slot] = ys.value
	vm.stack = vm.stack[:slot+1]
	return true
}
