//go:build dragonfly || freebsd || netbsd || openbsd

package run

import "syscall"

// killGroup kills pgid's group with one signal. FreeBSD made a group kill atomic
// against a concurrent fork in 2023 (pg_killsx, sys/kern/kern_sig.c); older FreeBSD
// can miss a child forked during the kill. A descendant that left the group through
// setsid or setpgid is not reached.
//
// Not procctl(PROC_REAP_KILL), FreeBSD's tree kill: it covers only a reaper's
// descendants, and making magus the reaper reparents every orphan of every child to
// magus, where reaping them with wait(-1) races os/exec's per-pid Wait.
//
// TODO: OpenBSD, NetBSD and DragonFly are unverified against the killpg/fork race.
// If one of them misses a forked child, give it darwin's list-and-kill loop, which
// needs a kinfo_proc decoder x/sys/unix does not provide there.
func killGroup(pgid int) { _ = syscall.Kill(-pgid, syscall.SIGKILL) }
