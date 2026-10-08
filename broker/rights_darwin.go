package broker

// recvFlags is empty: darwin's recvmsg has no MSG_CMSG_CLOEXEC, so keep marks each
// descriptor close-on-exec right after it arrives instead.
const recvFlags = 0
