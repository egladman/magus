//go:build darwin && !noffi

package mem

// This file is the DYNAMIC half of AvailableBytes. purego's cgo_import_dynamic
// gives a binary a PT_INTERP, which is exactly what the `noffi` tag exists to keep
// out of the archives magus ships as static (see release-build in magusfile.buzz).
// available_vmstat_darwin.go is the half that build gets instead.

import (
	"context"
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The Mach VM statistics call, and where the three counts sit inside its reply.
//
// HOST_VM_INFO64_COUNT is a count of natural_t (uint32) words, not bytes, which is
// why the buffer below is [62]uint32. The indices are the byte offsets from
// mach/vm_statistics.h divided by four, and they are NOT contiguous: speculative
// sits at byte 92, well past the uint64 counters in the middle of the struct.
// Measured against the SDK header on darwin/arm64.
const (
	hostVMInfo64      = 4
	hostVMInfo64Count = 62
	idxFree           = 0  // free_count,        byte 0
	idxInactive       = 2  // inactive_count,    byte 8
	idxSpeculative    = 23 // speculative_count, byte 92
)

// mach_host_self and host_statistics64 come from libSystem through purego, which
// needs no cgo and is already how Buzz FFI reaches a shared library here.
//
// The host port is resolved once: mach_host_self takes a reference per call, so
// calling it every two seconds for the life of a run would leak port rights.
var (
	machHostSelf = sync.OnceValue(func() uintptr {
		fn, err := purego.Dlsym(purego.RTLD_DEFAULT, "mach_host_self")
		if err != nil {
			return 0
		}
		port, _, _ := purego.SyscallN(fn)
		return port
	})
	hostStatistics64 = sync.OnceValue(func() uintptr {
		fn, err := purego.Dlsym(purego.RTLD_DEFAULT, "host_statistics64")
		if err != nil {
			return 0
		}
		return fn
	})
)

// AvailableBytes approximates MemAvailable as (free + inactive + speculative)
// pages, or 0 when the statistics cannot be read.
//
// Darwin publishes no MemAvailable equivalent. Inactive pages are the closest
// analogue to Linux's reclaimable cache; wired and active pages are excluded
// because a new allocation cannot have them without paging.
//
// Read from the kernel directly rather than by parsing vm_stat(1), which is what
// this used to do. Same source (vm_stat prints host_statistics64) without a fork
// per sample, and without a text format to misparse: the old version had to
// recover the page size from a header line and bail unless all three counters
// were found, because a partial parse reads as a crisis.
//
// A watchdog input, so it may be approximate. It may not be confidently wrong in
// the tight direction, which is why any failure yields 0 rather than a partial sum.
func AvailableBytes(_ context.Context) int64 {
	host, fn := machHostSelf(), hostStatistics64()
	if host == 0 || fn == 0 {
		return 0
	}
	var vm [hostVMInfo64Count]uint32
	count := uint32(hostVMInfo64Count)
	kr, _, _ := purego.SyscallN(fn, host, hostVMInfo64,
		uintptr(unsafe.Pointer(&vm[0])), uintptr(unsafe.Pointer(&count)))
	// KERN_SUCCESS is 0. A short reply is refused for the same reason a partial
	// parse was: the counters this needs are at the far end of the struct.
	if kr != 0 || count < hostVMInfo64Count {
		return 0
	}
	pages := int64(vm[idxFree]) + int64(vm[idxInactive]) + int64(vm[idxSpeculative])
	return pages * int64(os.Getpagesize())
}
