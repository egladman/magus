//go:build !linux && !darwin

package hostmem

// Available reports 0: no portable way to ask on the remaining hosts.
//
// Zero means UNKNOWN, and the watchdog stays silent rather than guessing. A
// fabricated figure would be worse than none: too high and the watchdog never
// fires while looking like it is watching, too low and it reports an emergency on
// every run until its reader learns to ignore it.
func Available() int64 { return 0 }
