package spawn

import (
	"golang.org/x/sys/unix"
)

// darwinTable is one kern.proc.all read: darwin has no per-process child list,
// so parentage is known only from the whole table.
type darwinTable struct {
	byPID    map[int]procInfo
	byParent map[int][]procInfo
}

func readProcTable() (procTable, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	t := darwinTable{byPID: make(map[int]procInfo, len(procs)), byParent: make(map[int][]procInfo, len(procs))}
	for i := range procs {
		p := procInfo{
			pid:    int(procs[i].Proc.P_pid),
			ppid:   int(procs[i].Eproc.Ppid),
			start:  procs[i].Proc.P_starttime.Sec*1_000_000 + int64(procs[i].Proc.P_starttime.Usec),
			comm:   commName(procs[i].Proc.P_comm[:]),
			zombie: procs[i].Proc.P_stat == szomb,
		}
		t.byPID[p.pid] = p
		t.byParent[p.ppid] = append(t.byParent[p.ppid], p)
	}
	return t, nil
}

func (t darwinTable) lookup(pid int) (procInfo, bool) {
	p, ok := t.byPID[pid]
	return p, ok
}

func (t darwinTable) children(pid int) []procInfo { return t.byParent[pid] }

func commName(raw []byte) string {
	for i, c := range raw {
		if c == 0 {
			return string(raw[:i])
		}
	}
	return string(raw)
}
