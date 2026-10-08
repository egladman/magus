package broker

import "golang.org/x/sys/unix"

// recvFlags marks a received descriptor close-on-exec as it arrives, so a service the
// broker starts can never inherit a client's stderr and hold it open.
const recvFlags = unix.MSG_CMSG_CLOEXEC
