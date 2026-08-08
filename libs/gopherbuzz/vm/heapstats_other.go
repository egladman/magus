//go:build buzz_safe || buzz_unsafe

package vm

// HeapStats reports 0, 0 under the safe and unsafe value representations: neither
// has a global heap table, so there is no object count to report and none of the
// pinning the nanbox build's HeapStats exists to diagnose can occur.
func HeapStats() (objects int, peak int) { return 0, 0 }
