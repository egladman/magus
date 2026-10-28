//go:build windows

package mem

import (
	"context"
	"unsafe"

	"golang.org/x/sys/windows"
)

// limitBytes reports the memory ceiling of the job object this process runs in (where
// `docker run --memory` lands on Windows), or 0 for none or on failure. A NULL handle
// asks about the current process's own job, which needs no privileges and no handle.
func limitBytes(_ context.Context) int64 {
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
