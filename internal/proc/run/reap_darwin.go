package run

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// szomb is a zombie's p_stat (sys/proc.h), which x/sys/unix does not name.
const szomb = 5

// member names a process across pid reuse.
type member struct {
	pid   int32
	start unix.Timeval
}

// killGroup kills pgid's group, best-effort. XNU's killpg1 signals a pid snapshot
// pgrp_iterate takes under the pgrp lock (bsd/kern/kern_sig.c), and fork1
// (bsd/kern/kern_fork.c) aborts a child only if its parent already has P_LEXIT, so a
// child forked across the snapshot escapes. This is FreeBSD's in-kernel reaper kill
// (reap_kill_subtree, sys/kern/kern_procctl.c) done from user space: list the group
// through the kern.proc.pgrp sysctl, SIGKILL each live member not yet signalled, and
// stop at a round that finds none, so a member that is only still exiting is not
// waited on. The unreaped leader is a zombie and holds the group's id.
//
// It misses a child whose parent was signalled mid-fork and is inserted after the last
// round, and any descendant that left the group through setsid or setpgid: darwin
// has no primitive that kills a process tree.
func killGroup(pgid int) {
	var signalled map[member]bool
	for {
		procs, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
		if err != nil {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			return
		}
		fresh := false
		for _, p := range procs {
			if p.Proc.P_stat == szomb {
				continue
			}
			m := member{pid: p.Proc.P_pid, start: p.Proc.P_starttime}
			if signalled[m] {
				continue
			}
			if signalled == nil {
				signalled = map[member]bool{}
			}
			signalled[m] = true
			fresh = true
			_ = syscall.Kill(int(m.pid), syscall.SIGKILL)
		}
		if !fresh {
			return
		}
	}
}
