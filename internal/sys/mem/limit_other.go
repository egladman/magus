//go:build !linux && !windows

package mem

import "context"

// LimitBytes reports 0: no memory ceiling to read on the remaining hosts.
//
// darwin is the one worth explaining, because it is where magus is most often
// developed and the answer looks like an omission. macOS imposes no per-process
// memory cap a build would run under, and Docker Desktop runs its containers
// inside a LINUX virtual machine, so a magus inside one is a linux binary
// reading the cgroup file like any other. There is nothing a darwin build could
// ask that the linux build is not already asking.
func LimitBytes(_ context.Context) int64 { return 0 }
