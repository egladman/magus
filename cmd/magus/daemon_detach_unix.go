//go:build !windows

package main

import "syscall"

// daemonSysProcAttr detaches the auto-backgrounded daemon from the launching shell.
// Setsid puts the child in its own session with no controlling terminal, so it survives
// the parent process exiting and a terminal hangup - the same isolation `setsid` or a
// process supervisor would give it.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
