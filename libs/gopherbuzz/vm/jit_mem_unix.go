//go:build (amd64 || arm64) && !windows && !buzz_safe && !buzz_unsafe

package vm

import "golang.org/x/sys/unix"

// mapExecutable copies buf into a fresh anonymous mapping, flips it to RX, and
// returns it, or nil if any step fails (the caller then runs the interpreter).
// The mapping lives until its Chunk is collected; see unmapExecutable.
//
// Split from the backends so the arch-specific code generators say nothing about
// how the OS hands out executable pages; jit_mem_windows.go is the other half.
func mapExecutable(buf []byte) []byte {
	mem, err := unix.Mmap(-1, 0, len(buf),
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_PRIVATE|unix.MAP_ANONYMOUS)
	if err != nil {
		return nil
	}
	copy(mem, buf)
	if err := unix.Mprotect(mem, unix.PROT_READ|unix.PROT_EXEC); err != nil {
		_ = unix.Munmap(mem)
		return nil
	}
	jitMappedBytes.Add(int64(len(mem)))
	// Mprotect does not invalidate the I-cache, so architectures that are not
	// I-cache coherent need explicit maintenance before the bytes are fetched as
	// code. No-op on amd64; the real sequence on arm64.
	flushICache(&mem[0], len(mem))
	return mem
}

// unmapExecutable releases a mapping mapExecutable returned. Called ONLY from the
// cache eviction that a chunk's death triggers, which is what makes it safe: while
// a chunk is running, it is reachable from vm.frames, so it cannot have been
// collected and this cannot fire underneath executing code. Calling it at any
// other point would be a use-after-free of the instruction stream.
//
// A failed unmap leaks the pages rather than propagating: there is no caller in a
// position to do anything about it, and the alternative (leaving the entry in the
// cache) would leak the chunk as well.
func unmapExecutable(mem []byte) {
	if len(mem) == 0 {
		return
	}
	if unix.Munmap(mem) != nil {
		return
	}
	jitMappedBytes.Add(-int64(len(mem)))
}
