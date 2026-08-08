//go:build !linux && !darwin

package hostmem

// Total reports 0: no portable way to ask on the remaining hosts.
//
// Zero means UNKNOWN, and the planner treats it as "no budget", which plans
// exactly as it did before a budget existed. A guess would be worse: too high
// protects nothing while looking like it does, and too low buys runners nobody
// needed on a machine nobody measured.
func Total() int64 { return 0 }
