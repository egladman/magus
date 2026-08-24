// Package mem reads the memory of the machine magus is running on, and of the
// process trees it starts there.
//
// It sits under internal/sys because that is what it is: the layer that asks the
// operating system about itself, in the sense golang.org/x/sys and syscall use the
// word. Not to be confused with internal/memory, which is magus's own durable
// memory store, or with the host modules in std/, which are "host" in the
// language-embedding sense.
//
// TotalBytes is a property of the machine class and is what the CI shard planner
// budgets against. UsableBytes narrows that by any ceiling this process actually
// runs under, such as a container's. AvailableBytes and SwapUsedBytes are
// properties of this moment and are what the run-time watchdog watches. TreeBytes
// totals a live process tree, which is the one figure the kernel will not fold for
// us. All return 0 for UNKNOWN; callers must branch on that rather than read it as
// "no memory".
package mem
