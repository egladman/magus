//go:build linux

package mem

import (
	"os"
	"strconv"
	"strings"
)

// TreeBytes sums the resident memory of pid and every descendant, or 0 when the
// tree cannot be read.
//
// /proc/<pid>/task/<tid>/children rather than a scan of the whole table: the
// kernel already maintains the child list, so the walk touches only the tree it is
// asked about. A pid that exits mid-walk simply stops contributing.
func TreeBytes(pid int) int64 {
	var total int64
	seen := map[int]bool{}
	stack := []int{pid}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		total += procRSS(cur)
		stack = append(stack, procChildren(cur)...)
	}
	return total
}

// procRSS reads the resident pages out of /proc/<pid>/statm and converts them to
// bytes, or 0 when the process is gone.
//
// statm rather than status or smaps_rollup: it is a single short line of numbers
// with no keys to match, which is what makes it cheap enough to sample.
func procRSS(pid int) int64 {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || pages < 0 {
		return 0
	}
	return pages * int64(os.Getpagesize())
}

// procChildren lists a process's direct children, gathered across its threads
// because each thread keeps its own children file.
func procChildren(pid int) []int {
	base := "/proc/" + strconv.Itoa(pid) + "/task"
	tids, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []int
	for _, tid := range tids {
		raw, rerr := os.ReadFile(base + "/" + tid.Name() + "/children")
		if rerr != nil {
			continue
		}
		for _, f := range strings.Fields(string(raw)) {
			if n, cerr := strconv.Atoi(f); cerr == nil && n > 0 {
				out = append(out, n)
			}
		}
	}
	return out
}
