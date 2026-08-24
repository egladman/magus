//go:build darwin

package mem

import (
	"golang.org/x/sys/unix"
)

// procTaskInfo mirrors struct proc_taskinfo from sys/proc_info.h. Only the second
// field is read; the rest is declared so the size matches what the kernel expects,
// which it checks.
type procTaskInfo struct {
	VirtualSize   uint64
	ResidentSize  uint64
	TotalUser     uint64
	TotalSystem   uint64
	ThreadsUser   uint64
	ThreadsSystem uint64
	Policy        int32
	Faults        int32
	Pageins       int32
	CowFaults     int32
	MessagesSent  int32
	MessagesRecv  int32
	SyscallsMach  int32
	SyscallsUnix  int32
	Csw           int32
	Threadnum     int32
	Numrunning    int32
	Priority      int32
}

// procPidTaskInfo is PROC_PIDTASKINFO from sys/proc_info.h, the flavor that fills
// a procTaskInfo.
const procPidTaskInfo = 4

// TreeBytes sums the resident memory of pid and every descendant, or 0 when the
// tree cannot be read.
//
// One sysctl for the whole process table rather than a listchildpids call per node:
// the table is what makes parentage knowable, and asking once is cheaper than
// asking per process on a machine running hundreds.
//
// The sizes come from procRSS, which is split across two files by build tag: the
// process table gives parentage but no sizes, because kinfo_proc's Vmspace has
// every field zeroed by Apple.
func TreeBytes(pid int) int64 {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil || len(procs) == 0 {
		return 0
	}
	children := make(map[int32][]int32, len(procs))
	for i := range procs {
		self := procs[i].Proc.P_pid
		parent := procs[i].Eproc.Ppid
		children[parent] = append(children[parent], self)
	}
	var total int64
	// Iterative, because a pid table read live can in principle contain a cycle
	// (a pid reused as its own ancestor between the read and the walk), and a
	// recursive walk would not come back.
	seen := map[int32]bool{}
	stack := []int32{int32(pid)}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		total += procRSS(cur)
		stack = append(stack, children[cur]...)
	}
	return total
}
