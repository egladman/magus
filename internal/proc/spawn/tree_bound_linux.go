package spawn

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// linuxTable reads /proc on demand: the kernel keeps each process's child
// list, so a walk touches only the trees it follows.
type linuxTable struct{}

// readProcTable refuses a kernel built without CONFIG_PROC_CHILDREN: with no
// child lists every walk would find the root alone and bound nothing.
func readProcTable() (linuxTable, error) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		return linuxTable{}, err
	}
	self := fmt.Sprintf("/proc/self/task/%d/children", os.Getpid())
	if _, err := os.Stat(self); err != nil {
		return linuxTable{}, fmt.Errorf("no /proc/<pid>/task/<tid>/children, so this kernel lacks CONFIG_PROC_CHILDREN: %w", err)
	}
	return linuxTable{}, nil
}

// lookup parses /proc/<pid>/stat: the comm sits in parentheses and may hold
// spaces, so the fields are read after the last ')'. A stat it cannot parse
// is no process, rather than one with start 0 that every check would pass.
func (linuxTable) lookup(pid int) (procInfo, bool) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
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
	ppid, perr := strconv.Atoi(fields[1])
	start, serr := strconv.ParseInt(fields[19], 10, 64)
	if perr != nil || serr != nil {
		return procInfo{}, false
	}
	return procInfo{pid: pid, ppid: ppid, start: start, comm: s[open+1 : end], zombie: fields[0] == "Z"}, true
}

func (t linuxTable) children(pid int) []procInfo {
	tasks, _ := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", pid))
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

// marked reads the environment of every process started at or after since.
// Another user's environ is unreadable, and such a process carries no mark of
// this one's.
func (t linuxTable) marked(mark string, since int64) []procInfo {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	prefix := []byte(TreeMarkEnv + "=")
	var out []procInfo
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		p, ok := t.lookup(pid)
		if !ok || p.zombie || p.start < since {
			continue
		}
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
		if err != nil {
			continue
		}
		for _, kv := range bytes.Split(raw, []byte{0}) {
			if v, ok := bytes.CutPrefix(kv, prefix); ok && hasMark(string(v), mark) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}
