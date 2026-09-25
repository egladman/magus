package run

import (
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// szomb is a zombie's p_stat (sys/proc.h), which x/sys/unix does not name.
const szomb = 5

// killGroup kills pgid's group until nothing in it is alive. On darwin a process the
// group forks while a group kill is delivered escapes it, so the kill is repeated while
// the kernel lists a live member; the unreaped leader is a zombie and does not count.
// It gives up after about 100ms, leaving what the kernel would not end.
func killGroup(pgid int) {
	for range 100 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		procs, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
		if err != nil || !anyAlive(procs) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func anyAlive(procs []unix.KinfoProc) bool {
	for _, p := range procs {
		if p.Proc.P_stat != szomb {
			return true
		}
	}
	return false
}
