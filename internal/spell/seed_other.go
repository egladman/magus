//go:build !darwin

package spell

import (
	"errors"
	"os"
)

const cloneSupported = false

var errNoTreeClone = errors.New("no whole-tree clone on this platform")

func cloneTree(_, _ string) error { return errNoTreeClone }

func renameExclusive(_, _ string) error { return errNoTreeClone }

// acquireSeedLock and seedAbandoned are unreachable while cloneSupported is false:
// SeedInstall returns before calling either. Kept as no-ops, not a "PID is alive" guess
// like the one this replaced, so the day this platform gains a clone strategy the
// reclaim path does not silently trust something it never checked.
func acquireSeedLock(_ string) (*os.File, error) { return nil, errNoTreeClone }

func seedAbandoned(_ string) bool { return false }
