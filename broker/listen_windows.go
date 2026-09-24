package broker

import "golang.org/x/sys/windows"

// errAddrInUse is Winsock's spelling, which syscall.EADDRINUSE does not match.
var errAddrInUse error = windows.WSAEADDRINUSE
