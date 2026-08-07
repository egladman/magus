//go:build windows || wasm

package run

import "os"

// maxRSSBytes reports 0: neither host exposes a POSIX rusage for a finished child.
//
// Zero means UNKNOWN, not "used no memory", and every consumer treats it that way -
// reporting a confident 0 B would be worse than reporting nothing, because a reader
// cannot tell an unmeasured run from a free one.
func maxRSSBytes(_ *os.ProcessState) int64 { return 0 }
