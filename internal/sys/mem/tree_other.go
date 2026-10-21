//go:build !linux && !darwin && !windows

package mem

// TreeBytes reports 0 for every pid: no process table to walk on the remaining hosts
// (wasm being the one magus actually builds for). Zero means UNKNOWN, and every
// caller falls back to whatever the kernel reported for the direct child.
func TreeBytes(pids ...int) []int64 { return make([]int64, len(pids)) }
