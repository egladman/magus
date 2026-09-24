//go:build !windows

package broker

import "syscall"

// errAddrInUse is syscall.EADDRINUSE everywhere but Windows.
var errAddrInUse error = syscall.EADDRINUSE
