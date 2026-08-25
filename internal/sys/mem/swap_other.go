//go:build !linux && !darwin

package mem

import "context"

// SwapUsedBytes reports 0: no portable way to ask on the remaining hosts, so the
// watchdog watches headroom alone rather than guessing. See total_other.go.
func SwapUsedBytes(_ context.Context) int64 { return 0 }
