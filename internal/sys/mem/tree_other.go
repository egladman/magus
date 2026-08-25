//go:build !linux && !darwin && !windows

package mem

// TreeBytes reports 0: no process table to walk on the remaining hosts (wasm being
// the one magus actually builds for). Zero means UNKNOWN, and every caller falls
// back to whatever the kernel reported for the direct child.
func TreeBytes(_ int) int64 { return 0 }
