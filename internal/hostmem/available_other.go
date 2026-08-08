//go:build !linux && !darwin

package hostmem

// Available reports 0: no portable way to ask on the remaining hosts, so the
// watchdog stays silent rather than guessing. See total_other.go.
func Available() int64 { return 0 }
