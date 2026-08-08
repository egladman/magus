//go:build (amd64 || arm64) && !windows && !buzz_safe && !buzz_unsafe

package vm

import "golang.org/x/sys/unix"

// mapExecutable copies buf into a fresh anonymous mapping, flips it to RX, and
// returns it, or nil if any step fails (the caller then runs the interpreter).
// The mapping is never unmapped: a compiledJIT is cached for the life of its
// Chunk, and the entry pointer handed to generated code must stay valid.
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
	// Mprotect does not invalidate the I-cache, so architectures that are not
	// I-cache coherent need explicit maintenance before the bytes are fetched as
	// code. No-op on amd64; the real sequence on arm64.
	flushICache(&mem[0], len(mem))
	return mem
}
