package vm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fillRegistry puts n placeholder entries in vm's registry, enough to arm the
// automatic sweep.
func fillRegistry(vm *VM, n int) {
	// A "collect" entry with a nil Fn is callCollector's no-op, so a sweep can run
	// over these without any of them executing Buzz.
	def := &objectDefObj{Methods: []methodEntry{{Name: "collect"}}}
	for i := 0; i < n; i++ {
		vm.collectables = append(vm.collectables, &objectInst{Def: def})
	}
}

// TestMaybeCollectLeavesTheThresholdAloneWhenTheSweepIsDeclined pins the one thing
// CollectUnreachable's (int, error) cannot say: that it DECLINED.
//
// A nested sweep returns (0, nil), which is indistinguishable from "swept, nothing
// was unreachable". maybeCollect read that as a completed sweep and reset the
// threshold anyway. The path is live: a collector runs on a VM parented to this
// tree, so an instance it allocates reaches maybeCollect while the enclosing sweep
// is still running, and root.collectables is still at its full pre-sweep length -
// so the threshold doubled off a count that was about to shrink, permanently
// loosening the bound the automatic sweep exists to enforce, once per allocation
// per collector.
func TestMaybeCollectLeavesTheThresholdAloneWhenTheSweepIsDeclined(t *testing.T) {
	vm := NewVM(context.Background())
	fillRegistry(vm, gcMinThreshold)
	vm.gcSweeping = true // an enclosing sweep is mid-flight

	before := vm.gcThreshold
	require.NoError(t, vm.maybeCollect())
	assert.Equal(t, before, vm.gcThreshold,
		"a declined sweep collected nothing, so the threshold has no new live count to double")
	assert.Len(t, vm.collectables, gcMinThreshold, "and it must not have dropped anything")
}

// TestMaybeCollectRaisesTheThresholdAfterARealSweep is the other half: when a sweep
// does run, the threshold must move, or a program legitimately holding N
// collectables sweeps on every allocation past the minimum.
func TestMaybeCollectRaisesTheThresholdAfterARealSweep(t *testing.T) {
	vm := NewVM(context.Background())
	fillRegistry(vm, gcMinThreshold)

	require.NoError(t, vm.maybeCollect())
	assert.Equal(t, gcMinThreshold, vm.gcThreshold,
		"nothing survived, so the threshold returns to its floor")

	// Now with survivors: everything stays live, so the next sweep is deferred until
	// the registry has roughly doubled.
	vm.collectables = nil
	vm.gcThreshold = gcMinThreshold
	fillRegistry(vm, gcMinThreshold)
	live := make([]Value, 0, gcMinThreshold)
	for _, inst := range vm.collectables {
		live = append(live, heapValue(tagObject, inst))
	}
	vm.stack = append(vm.stack, live...)

	require.NoError(t, vm.maybeCollect())
	assert.Equal(t, 2*gcMinThreshold, vm.gcThreshold,
		"every entry survived, so the threshold doubles off the live count")
	assert.Len(t, vm.collectables, gcMinThreshold, "and every entry is still tracked")
}

// TestExplicitCollectLowersTheThreshold pins why the recompute lives in
// CollectUnreachable rather than in maybeCollect. An automatic sweep can raise the
// threshold a long way; an explicit gc\collect() that then empties the registry
// used to leave it there, so automatic sweeping stayed off until the registry
// regrew to the old mark.
func TestExplicitCollectLowersTheThreshold(t *testing.T) {
	vm := NewVM(context.Background())
	vm.gcThreshold = 8192 // as a previous automatic sweep over a large live set left it
	fillRegistry(vm, 8)   // far below it, so maybeCollect would not fire

	n, err := vm.CollectUnreachable()
	require.NoError(t, err)
	assert.Equal(t, 8, n, "all eight were unreachable")
	assert.Equal(t, gcMinThreshold, vm.gcThreshold,
		"the registry is empty now, so the threshold returns to its floor")
}

// TestCollectUnreachableDeclinesWhileSweeping pins the guard itself. Re-entering
// would walk a registry the enclosing sweep is midway through and call collect() a
// second time on an instance already collected.
func TestCollectUnreachableDeclinesWhileSweeping(t *testing.T) {
	vm := NewVM(context.Background())
	fillRegistry(vm, 4)
	vm.gcSweeping = true

	n, err := vm.CollectUnreachable()
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Len(t, vm.collectables, 4, "the enclosing sweep still owns these")
}

// TestGCRootWalksToTheOwningVM covers the registry lookup a fiber tree shares.
func TestGCRootWalksToTheOwningVM(t *testing.T) {
	root := NewVM(context.Background())
	fiber := newFiberVM(root)
	nested := newFiberVM(fiber)

	assert.Same(t, root, root.gcRoot(), "a root VM owns its own registry")
	assert.Same(t, root, fiber.gcRoot())
	assert.Same(t, root, nested.gcRoot(), "however deep the fiber nesting")

	// Tracking through any VM in the tree lands in the one registry.
	nested.collectables = nil
	inst := &objectInst{Def: &objectDefObj{Methods: []methodEntry{{Name: "collect"}}}}
	nested.trackCollectable(inst)
	assert.Len(t, root.collectables, 1, "tracked on the root, not the fiber")
	assert.Empty(t, nested.collectables)
}
