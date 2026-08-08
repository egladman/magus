//go:build (amd64 || arm64) && windows && !buzz_safe && !buzz_unsafe

package vm

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// FlushInstructionCache is not exposed by x/sys/windows, so bind it here.
// NewLazySystemDLL (rather than NewLazyDLL) resolves out of the system directory
// only, so a kernel32.dll planted next to the executable cannot be loaded instead.
var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procFlushInstructionCache = kernel32.NewProc("FlushInstructionCache")
)

// mapExecutable copies buf into a fresh private commit, flips it to RX, and
// returns it as a slice aliasing that memory, or nil if any step fails (the
// caller then runs the interpreter). The allocation is never released: a
// compiledJIT is cached for the life of its Chunk, and the entry pointer handed
// to generated code must stay valid.
//
// Windows has no mmap, and allocating RWX in one call is what W^X-enforcing
// mitigations (and some EDR products) block, so this follows the same
// write-then-reprotect shape as the unix half in jit_mem_unix.go.
func mapExecutable(buf []byte) []byte {
	n := uintptr(len(buf))
	addr, err := windows.VirtualAlloc(0, n, windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if err != nil {
		return nil
	}
	// Sound for the reason vet's unsafeptr check exists to protect: this address is
	// an OS reservation outside the Go heap, so the collector neither tracks nor
	// moves it, and it stays mapped for the process lifetime. VirtualAlloc hands back
	// a uintptr and there is no variant that does not.
	//
	// KNOWN PAPERCUT: `go vet` still flags it, and nothing silences that - //nolint is
	// golangci-lint's directive, not vet's. So `magus run lint libs/gopherbuzz` fails
	// for a contributor working ON Windows. CI lints on ubuntu, where this file is not
	// built, and `go test`'s vet subset omits unsafeptr, so neither gate sees it.
	mem := unsafe.Slice((*byte)(unsafe.Pointer(addr)), len(buf))
	copy(mem, buf)

	var old uint32
	if err := windows.VirtualProtect(addr, n, windows.PAGE_EXECUTE_READ, &old); err != nil {
		_ = windows.VirtualFree(addr, 0, windows.MEM_RELEASE)
		return nil
	}
	// Required on Windows on ARM, whose instruction fetch is not coherent with the
	// data writes above. Documented as mandatory after generating code on ANY
	// architecture, so it is called unconditionally rather than gated on arm64;
	// on x86 the kernel makes it cheap. Unlike the unix half this uses the Win32
	// API rather than emitting cache-maintenance instructions directly, because
	// whether those are permitted at EL0 is the OS's business, not ours.
	// CurrentProcess, not GetCurrentProcess: the pseudo-handle is a constant, so the
	// latter's error is unconditionally nil and its branch unreachable.
	if r, _, _ := procFlushInstructionCache.Call(uintptr(windows.CurrentProcess()), addr, n); r == 0 {
		_ = windows.VirtualFree(addr, 0, windows.MEM_RELEASE)
		return nil
	}
	return mem
}
