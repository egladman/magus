package spawn

import (
	"bytes"
	"encoding/binary"

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

// marked reads each candidate's environment through kern.procargs2, which
// answers only for this user's processes.
func (t darwinTable) marked(mark string, since int64) []procInfo {
	var out []procInfo
	for _, p := range t.byPID {
		if p.zombie || p.start < since {
			continue
		}
		raw, err := unix.SysctlRaw("kern.procargs2", p.pid)
		if err != nil {
			continue
		}
		if v, ok := procargsEnv(raw, TreeMarkEnv); ok && hasMark(v, mark) {
			out = append(out, p)
		}
	}
	return out
}

// procargsEnv finds name in a kern.procargs2 buffer. It holds argc, the exec
// path with NUL padding, argc arguments, then the environment.
func procargsEnv(raw []byte, name string) (string, bool) {
	if len(raw) < 4 {
		return "", false
	}
	argc := int(binary.LittleEndian.Uint32(raw))
	fields := bytes.Split(raw[4:], []byte{0})
	// fields[0] is the exec path; the padding after it splits into empties.
	i := 1
	for i < len(fields) && len(fields[i]) == 0 {
		i++
	}
	prefix := []byte(name + "=")
	for _, f := range fields[min(i+argc, len(fields)):] {
		if len(f) == 0 {
			return "", false
		}
		if v, ok := bytes.CutPrefix(f, prefix); ok {
			return string(v), true
		}
	}
	return "", false
}

func commName(raw []byte) string {
	for i, c := range raw {
		if c == 0 {
			return string(raw[:i])
		}
	}
	return string(raw)
}
