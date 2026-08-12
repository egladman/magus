package vm

import "context"

// Buzz's collector callback, implemented as a reachability sweep over the VM's own
// value graph rather than over Go's heap.
//
// Upstream calls an object's `collect()` when the object becomes garbage. Go's GC
// cannot provide that: runtime.SetFinalizer runs on its own goroutine at an
// unspecified later time, and this VM is single-goroutine, so a finalizer could
// neither call back safely nor arrive before `gc\collect()` returned. The
// assertion upstream writes - collect, then immediately check the callback ran -
// is unsatisfiable that way.
//
// What IS knowable synchronously is Buzz reachability: the VM owns its roots
// (globals, the operand stack, every frame's env and receiver), so it can mark
// what a program can still reach and treat the rest as garbage. Go still owns the
// memory; this decides only WHEN a collect() callback fires. An object the VM has
// dropped may well still be alive in Go, and that is fine - the callback is about
// the program's view, not the allocator's.

// vmCtxKey retrieves the running VM from the context a host callable receives.
type vmCtxKey struct{}

// FromContext returns the VM whose call is in progress, or nil. A host module needs
// it for the rare operation that is about the interpreter itself rather than about
// its arguments - `gc\collect()` is the whole of that set today.
func FromContext(ctx context.Context) *VM {
	v, _ := ctx.Value(vmCtxKey{}).(*VM)
	return v
}

// trackCollectable records an instance whose type declares a `collect()` method, so
// CollectUnreachable can find it later. Instances without one are not tracked:
// nothing would ever be called on them, and the registry is walked per collection.
func (vm *VM) trackCollectable(inst *objectInst) {
	if inst == nil || inst.Def == nil {
		return
	}
	for _, m := range inst.Def.Methods {
		if m.Name == "collect" {
			vm.collectables = append(vm.collectables, inst)
			return
		}
	}
}

// CollectUnreachable calls `collect()` on every tracked instance the program can no
// longer reach, and returns how many it called.
//
// Sweeping the registry rather than the heap keeps the cost proportional to the
// number of objects that actually declare a collector, which in practice is nearly
// none. A collected instance is dropped from the registry, so its collect() runs at
// most once however many times this is called.
func (vm *VM) CollectUnreachable() (int, error) {
	if len(vm.collectables) == 0 {
		return 0, nil
	}
	live := map[*objectInst]bool{}
	seen := map[any]bool{}
	for i := range vm.stack {
		markReachable(vm.stack[i], live, seen)
	}
	for i := range vm.frames {
		markEnv(vm.frames[i].env, live, seen)
		markReachable(vm.frames[i].this, live, seen)
		if f := vm.frames[i].fun; f != nil {
			for _, uv := range f.Upvals {
				markReachable(uv, live, seen)
			}
		}
	}

	var kept []*objectInst
	var collected int
	for i, inst := range vm.collectables {
		if live[inst] {
			kept = append(kept, inst)
			continue
		}
		if err := vm.callCollector(inst); err != nil {
			// Keep it tracked: a collector that failed has not run to completion, and
			// silently dropping it would hide the failure on a later sweep.
			//
			// The TAIL has to survive too. kept holds only what this sweep has
			// visited, so assigning it alone deregistered every instance after the
			// failing one - none of their collect() methods would ever run, on this
			// sweep or any later one. Only the failure itself is early; the rest of
			// the registry is untouched work, not garbage.
			vm.collectables = append(append(kept, inst), vm.collectables[i+1:]...)
			return collected, err
		}
		collected++
	}
	vm.collectables = kept
	return collected, nil
}

// callCollector invokes an instance's `collect()` method.
func (vm *VM) callCollector(inst *objectInst) error {
	for _, m := range inst.Def.Methods {
		if m.Name != "collect" {
			continue
		}
		if m.Fn == nil {
			return nil
		}
		// Bound to this receiver: a collector reads `this`, and the entry in the
		// vtable is the unbound definition shared by every instance.
		fn := *m.Fn
		fn.This = heapValue(tagObject, inst)
		// callValue, not vm.Call + vm.Exec: this runs from inside a host call on a VM
		// that is already mid-execution, and driving that same VM re-entrantly walks
		// off its own frame stack. callValue is the path a map.filter callback takes,
		// and it runs the closure on a fresh VM sharing the environment.
		_, err := callValue(vm, vm.ctx, heapValue(tagFun, &fn), nil)
		return err
	}
	return nil
}

// markReachable walks a value, recording every object instance it can reach. seen
// guards against a cycle, which an object graph may freely contain.
func markReachable(v Value, live map[*objectInst]bool, seen map[any]bool) {
	switch v.tag() {
	case tagObject:
		inst := v.asObject()
		if live[inst] {
			return
		}
		live[inst] = true
		for _, f := range inst.Fields {
			markReachable(f, live, seen)
		}
	case tagList:
		lo := v.asList()
		if seen[lo] {
			return
		}
		seen[lo] = true
		for _, item := range lo.Items {
			markReachable(item, live, seen)
		}
	case tagMap:
		mo := v.asMap()
		if seen[mo] {
			return
		}
		seen[mo] = true
		for _, item := range mo.Vals {
			markReachable(item, live, seen)
		}
		// keyVals holds the real key VALUES, and a key may be an object. Walking
		// only Vals left an object used as a map key invisible to the mark, so
		// gc\collect() reclaimed it while the map still keyed on it.
		for _, k := range mo.keyVals {
			markReachable(k, live, seen)
		}
	case tagIterState:
		// The collection a foreach is walking lives ONLY in the iterator state
		// while the loop runs - it is not on the stack and not in any env. Without
		// this case, iterating three objects and calling gc\collect() inside the
		// body collected the elements the loop had not reached yet.
		is := v.asIterState()
		if is == nil || seen[is] {
			return
		}
		seen[is] = true
		if is.list != nil {
			for _, item := range is.list.Items {
				markReachable(item, live, seen)
			}
		}
		if is.mapObj != nil {
			for _, item := range is.mapObj.Vals {
				markReachable(item, live, seen)
			}
			for _, k := range is.mapObj.keyVals {
				markReachable(k, live, seen)
			}
		}
	case tagCell:
		markReachable(v.asCell().v, live, seen)
	case tagFun:
		fo := v.asFun()
		if seen[fo] {
			return
		}
		seen[fo] = true
		markReachable(fo.This, live, seen)
		for _, uv := range fo.Upvals {
			markReachable(uv, live, seen)
		}
	}
}

// markEnv walks an environment chain and everything it holds.
func markEnv(env *Env, live map[*objectInst]bool, seen map[any]bool) {
	for e := env; e != nil; e = e.parent {
		if seen[e] {
			return
		}
		seen[e] = true
		for i := range e.slots {
			markReachable(e.slots[i], live, seen)
		}
	}
}
