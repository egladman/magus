//go:build dragonfly || freebsd || netbsd || openbsd

package run

import (
	"syscall"
	"time"
)

// groupKills is how many times killGroup signals the group, a millisecond apart.
const groupKills = 3

// killGroup kills pgid's group, best-effort: a child forked across one kill is in the
// group for the next. FreeBSD closed the killpg/fork race in 2023 (pg_killsx), older
// FreeBSD did not, and OpenBSD, NetBSD and DragonFly are unverified. Unlike darwin's
// loop this cannot list the group, since x/sys/unix decodes no kinfo_proc here, so it
// pays the two sleeps on every call. A descendant that left the group through setsid
// or setpgid is not reached.
//
// Not procctl(PROC_REAP_KILL), FreeBSD's atomic tree kill: it covers only a reaper's
// descendants, and making magus the reaper reparents every orphan of every child to
// magus, where reaping them with wait(-1) races os/exec's per-pid Wait.
func killGroup(pgid int) {
	for i := range groupKills {
		if i > 0 {
			time.Sleep(time.Millisecond)
		}
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}
