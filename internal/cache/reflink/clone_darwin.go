package reflink

import (
	"errors"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// probe reports whether the filesystem containing dir supports CoW reflinks
// (clonefile). Returns false when dir doesn't exist, the probe files can't be
// created, or the filesystem is not APFS.
func probe(dir string) bool {
	src, err := os.CreateTemp(dir, ".reflink-probe-src.*")
	if err != nil {
		return false
	}
	srcName := src.Name()
	defer os.Remove(srcName)
	if _, err := src.Write([]byte("probe")); err != nil {
		src.Close()
		return false
	}
	src.Close()

	dstName := srcName + ".dst"
	defer os.Remove(dstName)
	if err := unix.Clonefile(srcName, dstName, 0); err == nil {
		return true
	}
	return false
}

// clone copies src to dst using the most efficient mechanism available
// on the current filesystem:
//
//  1. clonefile(2) (APFS): O(1) copy-on-write reflink — APFS exposes true
//     CoW semantics; writes to either file after cloning are isolated.
//  2. io.Copy: fallback for HFS+, network mounts, or any FS without
//     clonefile support.
//
// dst must not exist when Clone is called (the caller removes it first).
// clonefile itself requires a non-existent destination and will return
// EEXIST otherwise.
func clone(src, dst string) error {
	// ── Path 1: clonefile (APFS CoW) ──────────────────────────────────
	cloneErr := unix.Clonefile(src, dst, 0)
	if cloneErr == nil {
		return nil
	}
	// Only fall through to userspace copy on ENOTSUP/ENOSYS/EXDEV (filesystem
	// doesn't support clonefile); any other error is a real failure (EEXIST, ENOSPC, EIO…).
	if !errors.Is(cloneErr, syscall.ENOTSUP) && !errors.Is(cloneErr, syscall.ENOSYS) && !errors.Is(cloneErr, syscall.EXDEV) {
		return cloneErr
	}

	// ── Path 2: io.Copy ───────────────────────────────────────────────
	return copyIO(src, dst)
}

func copyIO(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// O_EXCL: fail if dst already exists rather than silently truncating it.
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		// Propagate Close error: ENOSPC on writeback flush is otherwise invisible.
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()

	_, err = io.Copy(out, in)
	return
}
