//go:build linux || dragonfly || freebsd || netbsd || openbsd

package run

import "syscall"

// killGroup kills pgid's group. Linux delivers a group kill to a child forked while it
// is sent, so one is enough there.
func killGroup(pgid int) { _ = syscall.Kill(-pgid, syscall.SIGKILL) }
