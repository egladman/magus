//go:build windows

package mem

import (
	"context"
	"unsafe"

	"golang.org/x/sys/windows"
)

// LimitBytes reports the job object's memory ceiling, or 0 when this process is
// in no job or the job caps nothing.
//
// Job objects are the Windows equivalent of the cgroup limit read on linux:
// process-isolated Windows containers run their payload inside one, and
// `docker run --memory` lands here rather than on any /proc-like file.
//
// A NULL handle asks about the job the CURRENT process belongs to, which is the
// documented way to query one's own limits and needs no elevated privileges and
// no handle to open. Failure is 0 (UNKNOWN), the same answer as a host that
// imposes no limit at all: both mean magus has nothing to narrow the budget with.
func LimitBytes(_ context.Context) int64 {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	var retlen uint32
	err := windows.QueryInformationJobObject(
		0, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), &retlen)
	if err != nil {
		return 0
	}
	// JobMemoryLimit caps the job as a whole, ProcessMemoryLimit caps each process
	// in it. Either one bounds what this build may commit, so the smaller of the
	// two that is actually set is the honest ceiling.
	limit := int64(0)
	flags := info.BasicLimitInformation.LimitFlags
	if flags&windows.JOB_OBJECT_LIMIT_JOB_MEMORY != 0 {
		limit = int64(info.JobMemoryLimit)
	}
	if flags&windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY != 0 {
		if n := int64(info.ProcessMemoryLimit); n > 0 && (limit == 0 || n < limit) {
			limit = n
		}
	}
	if limit < 0 {
		return 0
	}
	return limit
}
