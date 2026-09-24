//go:build !windows

package main

import "syscall"

// detachedSysProcAttr detaches a spawned broker or server from the launching shell.
// Setsid puts the child in its own session with no controlling terminal, so it survives
// the parent process exiting and a terminal hangup: the same isolation `setsid` or a
// process supervisor would give it.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
