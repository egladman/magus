//go:build !linux && !darwin

package mem

import "context"

// AvailableBytes reports 0: no portable way to ask on the remaining hosts, so the
// watchdog stays silent rather than guessing. See total_other.go.
func AvailableBytes(_ context.Context) int64 { return 0 }
