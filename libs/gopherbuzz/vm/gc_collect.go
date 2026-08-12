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

// gcMinThreshold is the smallest registry length that arms an automatic sweep.
// Only types declaring collect() are tracked and in practice nearly none do, so
// this is reached rarely; it exists to bound the program that allocates them in a
// loop and never calls gc\collect().
const gcMinThreshold = 256

// gcRoot returns the VM owning the fiber tree's registry. A fiber runs on a VM of
// its own but shares the program's object graph with its creator, so one registry
// and one root walk cover the tree. Per-VM registries would let a sweep inside a
// fiber judge an object the parent still holds unreachable and collect it.
func (vm *VM) gcRoot() *VM {
	for vm.gcParent != nil {
		vm = vm.gcParent
	}
	return vm
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
			root := vm.gcRoot()
			root.collectables = append(root.collectables, inst)
			return
		}
	}
}

// maybeCollect sweeps once the registry has outgrown its threshold, so a program
// that allocates collectables and never calls `gc\collect()` does not retain every
// one. Upstream fires collect() when an object becomes garbage rather than when the
// program asks, so an unrequested sweep is the closer behaviour.
//
// The caller must have made the new instance reachable first: a sweep run before
// the push finds it unreachable and collects it on the spot. A sweep nested inside
// one already running is declined by CollectUnreachable, the one place both this
// path and an explicit gc\collect() go through.
func (vm *VM) maybeCollect() error {
	root := vm.gcRoot()
	if len(root.collectables) < root.gcThreshold {
		return nil
	}
	_, err := vm.CollectUnreachable()
	return err
}

// CollectUnreachable calls `collect()` on every tracked instance the program can no
// longer reach, and returns how many it called.
//
// Sweeping the registry rather than the heap keeps the cost proportional to the
// number of objects that actually declare a collector, which in practice is nearly
// none. A collected instance is dropped from the registry, so its collect() runs at
// most once however many times this is called.
func (vm *VM) CollectUnreachable() (int, error) {
	root := vm.gcRoot()
	// A collector runs on a VM parented to this tree (see callCollector), so a
	// collectable it allocates can arm the automatic sweep, and a collector may also
	// call gc\collect() outright. Either way a nested sweep would walk the registry
	// the enclosing one is midway through and call collect() a second time on an
	// instance already collected, breaking the at-most-once guarantee. Declining is
	// correct rather than merely safe: the enclosing sweep is already collecting
	// everything currently unreachable.
	if root.gcSweeping || len(root.collectables) == 0 {
		return 0, nil
	}
	root.gcSweeping = true
	defer func() { root.gcSweeping = false }()
	live := map[*objectInst]bool{}
	seen := map[any]bool{}
	// Every VM from the requesting one up to the root is executing right now, so its
	// frames are roots whether or not its fiber handle is on an operand stack at this
	// instant - an inline `resume &work()` never binds one. Fibers reachable by value
	// are picked up by the mark itself (see markFib).
	for v := vm; v != nil; v = v.gcParent {
		markVMRoots(v, live, seen)
	}

	// Snapshot the registry: a collector may append to it (it runs on a parented VM),
	// and those entries are this sweep's OUTPUT, not its input. Ranging the snapshot
	// keeps the loop finite, and grown holds whatever arrived past it so the
	// reassignment below does not discard it.
	snapshot := root.collectables
	grown := func() []*objectInst { return root.collectables[len(snapshot):] }

	var kept []*objectInst
	var collected int
	for i, inst := range snapshot {
		if live[inst] {
			kept = append(kept, inst)
			continue
		}
		if err := vm.callCollector(inst); err != nil {
			// Keep it tracked: a collector that failed has not run to completion, and
			// silently dropping it would hide the failure on a later sweep.
			//
			// kept holds only what this sweep visited; snapshot[i:] is the untouched
			// tail, starting with the instance that just failed.
			root.collectables = append(append(kept, snapshot[i:]...), grown()...)
			return collected, err
		}
		collected++
	}
	root.collectables = append(kept, grown()...)
	// Amortised, so a program legitimately holding N collectables sweeps O(log N)
	// times rather than once per allocation past the threshold.
	//
	// Here rather than in maybeCollect, because BOTH paths reach this line and only
	// one reached that one. An explicit gc\collect() that emptied a registry the
	// automatic sweep had grown the threshold for used to leave the threshold high,
	// so automatic sweeping stayed off until the registry regrew to the old mark.
	// The early returns above are the decline and the error, neither of which swept,
	// and neither of which should move it.
	root.gcThreshold = max(gcMinThreshold, 2*len(root.collectables))
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
		// A fresh VM, not vm.Call + vm.Exec: this runs from inside a host call on a VM
		// that is already mid-execution, and driving that same VM re-entrantly walks
		// off its own frame stack. That is what callValue does for a map.filter
		// callback, and this is callValue's tagFun arm with one addition -- the VM is
		// PARENTED to this tree.
		//
		// The parent is why this is not just callValue. A collector body may allocate
		// a collectable of its own; on callValue's detached VM (gcParent nil, so its
		// own gcRoot) that instance joined a registry thrown away with the VM, and its
		// collect() never ran at all. Measured before the parent was added: 3000
		// collectors each allocating one instance produced ZERO collections of those
		// instances, even after two explicit gc\collect() calls.
		callVM := NewVM(vm.ctx)
		callVM.gcParent = vm.gcRoot()
		if err := callVM.Call(heapValue(tagFun, &fn), nil); err != nil {
			return err
		}
		_, err := callVM.Exec()
		return err
	}
	return nil
}

// markVMRoots walks one VM's roots: its operand stack and every frame's env,
// receiver and upvalues. A fiber owns a VM of its own, so this runs again for each
// reachable fiber (see markFib) rather than only for the VM being swept.
func markVMRoots(vm *VM, live map[*objectInst]bool, seen map[any]bool) {
	if vm == nil || seen[vm] {
		return
	}
	seen[vm] = true
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
}

// markFib walks a fiber: its cached return value and the whole VM it suspended on.
// A suspended fiber's locals are live - the program can still resume it - so its
// frames are roots for as long as the fiber handle itself is reachable.
func markFib(fb *fibObj, live map[*objectInst]bool, seen map[any]bool) {
	if fb == nil || seen[fb] {
		return
	}
	seen[fb] = true
	markReachable(fb.returnVal, live, seen)
	markVMRoots(fb.vm, live, seen)
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
		// A key may itself be an object; walking only Vals collected it in use.
		for _, k := range mo.keyVals {
			markReachable(k, live, seen)
		}
	case tagIterState:
		// While a foreach runs, its collection is reachable only from here.
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
		markFib(is.fib, live, seen)
	case tagFib:
		markFib(v.asFib(), live, seen)
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
