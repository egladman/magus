package spawn

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// linuxTable reads /proc on demand: the kernel keeps each process's child
// list, so a walk touches only the trees it follows.
type linuxTable struct{}

func readProcTable() (linuxTable, error) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		return linuxTable{}, err
	}
	return linuxTable{}, nil
}

// lookup parses /proc/<pid>/stat: the comm sits in parentheses and may hold
// spaces, so the fields are read after the last ')'.
func (linuxTable) lookup(pid int) (procInfo, bool) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return procInfo{}, false
	}
	s := string(raw)
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return procInfo{}, false
	}
	fields := strings.Fields(s[end+1:])
	// fields[0] is state, [1] ppid, [19] starttime (stat fields 3, 4 and 22).
	if len(fields) < 20 {
		return procInfo{}, false
	}
	ppid, _ := strconv.Atoi(fields[1])
	start, _ := strconv.ParseInt(fields[19], 10, 64)
	return procInfo{pid: pid, ppid: ppid, start: start, comm: s[open+1 : end], zombie: fields[0] == "Z"}, true
}

func (t linuxTable) children(pid int) []procInfo {
	tasks, _ := filepath.Glob(filepath.Join("/proc", strconv.Itoa(pid), "task", "*", "children"))
	var out []procInfo
	for _, f := range tasks {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, field := range strings.Fields(string(raw)) {
			child, err := strconv.Atoi(field)
			if err != nil {
				continue
			}
			if p, ok := t.lookup(child); ok {
				out = append(out, p)
			}
		}
	}
	return out
}
