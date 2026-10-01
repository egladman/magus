package vm

// Owner is the set of global heap-table slots one holder keeps, released
// together when the holder is done. A Session keeps one: every object its VMs
// allocate, every value a host hands it (globals, native modules, chunk
// constants) and every value a host callable returns into one of its VMs joins
// the set, and Session.Close releases them all in one pass.
//
// The table exists so a Value can be a pointerless uint64, which also makes a
// Value invisible to Go's collector: nothing but explicit release can ever
// reclaim a slot. Ownership is the lifetime rule that makes release safe: a
// slot belongs to the first owner that allocates or claims it, a later claim
// leaves it alone, and a Value stays valid until its owner releases. Values
// shared between sessions therefore live as long as the session that made
// them, which is why a session whose values every other session reads must not
// close.
//
// Strings are never owned: StrValue interns them process-wide and caches the
// slot on the interned object, so a freed string slot would be handed out again
// by the very next StrValue of that content.
//
// Only the default representation has a table to release from; the safe and
// unsafe builds carry each object as a Go pointer the collector reclaims, so
// there an Owner holds nothing. The zero value is ready to use, and a nil
// *Owner claims nothing.
type Owner struct {
	slots []uint64 // guarded by gHeapMu
}

// poisonReleased makes a released slot unusable instead of recyclable, so a
// Value read after its session closed panics; owner_race.go sets it.
var poisonReleased bool

// Claim takes each of vals and everything reachable from it that no owner
// holds yet, stopping at any slot already held.
func (o *Owner) Claim(vals ...Value) {
	if o != nil {
		heapClaim(o, vals)
	}
}

// Release frees every slot the owner holds and forgets them, after first taking
// every unowned slot reachable from roots. Claim stops at a held slot, so a
// value a host stored into a claimed container past the owner's notice (a
// method merged into a module table, an enum set on a namespace) is found only
// by this deeper walk, which looks through the owner's own slots; Session.Close
// passes its globals and native modules. Slots another owner holds still bound
// it. Releasing twice does nothing. Any Value into a released slot is dangling:
// the slot reads as nil until an allocation reuses it.
func (o *Owner) Release(roots ...Value) {
	if o != nil {
		heapRelease(o, roots)
	}
}

// ownable reports whether v is a heap Value an owner can hold: every heap kind
// but str.
func ownable(v Value) bool { return v.tag() > tagStr }

// SetOwner makes o the owner of everything vm allocates or adopts from here on.
// Child VMs (fibers, callbacks) inherit it from the VM on their context.
func (vm *VM) SetOwner(o *Owner) { vm.owner = o }

// adopt takes a value a host callable returned into vm. Host code builds values
// with the package-level constructors, which have no owner to charge, so the
// boundary where the value enters the VM is where it acquires one.
func (vm *VM) adopt(v Value) {
	if ownable(v) {
		vm.owner.Claim(v)
	}
}
