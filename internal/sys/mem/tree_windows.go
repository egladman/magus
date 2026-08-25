//go:build windows

package mem

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// processMemoryCounters mirrors PROCESS_MEMORY_COUNTERS from psapi.h. WorkingSetSize
// is the resident figure, the Windows counterpart of RSS.
type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// GetProcessMemoryInfo has no wrapper in x/sys/windows, so it is resolved lazily
// from psapi rather than pulling in another dependency for one call.
var (
	psapi                    = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
)

// TreeBytes sums the resident memory of pid and every descendant, or 0 when the
// tree cannot be read.
//
// This is the only platform where the figure exists at all: wait4-style rusage has
// no Windows equivalent, so maxRSSBytes reports UNKNOWN there and sampling is not a
// second opinion but the first one.
func TreeBytes(pid int) int64 {
	parents, err := processParents()
	if err != nil {
		return 0
	}
	children := make(map[uint32][]uint32, len(parents))
	for self, parent := range parents {
		children[parent] = append(children[parent], self)
	}
	var total int64
	seen := map[uint32]bool{}
	stack := []uint32{uint32(pid)}
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

// processParents maps every process to its parent from one toolhelp snapshot.
//
// The whole table in one call, because a snapshot is what Windows offers: there is
// no per-process child list to walk, and taking a snapshot per node would repeat
// the expensive part once per process.
func processParents() (map[uint32]uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	out := map[uint32]uint32{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		out[e.ProcessID] = e.ParentProcessID
	}
	return out, nil
}

// procRSS reports one process's working set in bytes, or 0 when it cannot be
// opened. A process that exited, or one this token may not query, both yield 0
// rather than failing the whole walk.
func procRSS(pid uint32) int64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)

	var pmc processMemoryCounters
	pmc.CB = uint32(unsafe.Sizeof(pmc))
	r, _, _ := procGetProcessMemoryInfo.Call(
		uintptr(h), uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.CB))
	if r == 0 {
		return 0
	}
	return int64(pmc.WorkingSetSize)
}
