//go:build linux && (amd64 || arm64)

// clone_linux.go is the FICLONE / copy_file_range path. Restricted to
// 64-bit Linux because the file-size → int conversion below relies on
// int being 64 bits. 32-bit Linux builds compile clone_linux_other.go,
// which delegates to the userspace io.Copy fallback.

package reflink

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Probe reports whether the filesystem containing dir supports CoW reflinks
// (FICLONE ioctl). Returns false when dir doesn't exist, the probe files
// can't be created, or the filesystem does not support FICLONE.
func Probe(dir string) bool {
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

	out, err := os.Create(dstName)
	if err != nil {
		return false
	}
	in, err := os.Open(srcName)
	if err != nil {
		out.Close()
		return false
	}
	defer in.Close()
	cloneErr := unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
	out.Close()
	return cloneErr == nil
}

// Clone copies src to dst using the most efficient mechanism available
// on the current filesystem:
//
//  1. FICLONE ioctl (btrfs, XFS, ext4 ≥ 4.5, OCFS2): O(1) copy-on-write
//     reflink — the fastest path and the only one with true CoW semantics.
//  2. copy_file_range(2): zero-copy in-kernel splice; still O(N) bytes
//     but avoids userspace buffering and works on any Linux ≥ 4.5.
//  3. io.Copy: read+write fallback for kernels that predate both of the
//     above.
//
// dst must not exist when Clone is called (the caller is responsible for
// removing it first). On success, dst has the same content as src.
func Clone(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// O_EXCL: fail if dst already exists rather than silently truncating it
	// (caller contract requires exclusive creation; darwin path agrees).
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
			// Best-effort: remove the partial destination file on error so
			// the caller can fall back without leaving a corrupt file behind.
			_ = os.Remove(dst)
		}
	}()

	// ── Path 1: FICLONE reflink ────────────────────────────────────────
	if cloneErr := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); cloneErr == nil {
		return nil
	}
	// EOPNOTSUPP / EINVAL / EXDEV: filesystem doesn't support reflinks, or
	// src and dst are on different devices. Fall through.

	// ── Path 2: copy_file_range (in-kernel, zero-copy) ────────────────
	inStat, err := in.Stat()
	if err != nil {
		return err
	}
	remain := inStat.Size()
	for remain > 0 {
		n, copyErr := unix.CopyFileRange(int(in.Fd()), nil, int(out.Fd()), nil, int(remain), 0)
		// Only fall through to userspace copy on ENOSYS/EXDEV/EOPNOTSUPP;
		// any other error (EIO, ENOSPC, etc.) is a real failure and must be returned.
		// Checked regardless of n: a kernel may report unsupported after partial
		// progress (n > 0), and the seek-to-0 below restarts the copy cleanly.
		if errors.Is(copyErr, unix.ENOSYS) || errors.Is(copyErr, unix.EXDEV) || errors.Is(copyErr, unix.EOPNOTSUPP) {
			// Kernel too old or filesystem unsupported — use userspace copy.
			// Assign to named return err so the deferred cleanup lambda sees it.
			if _, seekErr := in.Seek(0, io.SeekStart); seekErr != nil {
				err = seekErr
				return err
			}
			if _, seekErr := out.Seek(0, io.SeekStart); seekErr != nil {
				err = seekErr
				return err
			}
			_, err = io.Copy(out, in)
			return err
		}
		if copyErr != nil {
			err = copyErr
			return err
		}
		// copy_file_range returning (0, nil) means EOF from the kernel's
		// perspective; break rather than spinning indefinitely.
		if n == 0 {
			break
		}
		remain -= int64(n)
	}
	return nil
}
