package cache

// io_uring fast-path for batched file hashing on Linux. Submits IORING_OP_READ
// for files that missed the mtime cache; falls back to hashFile on per-file errors
// or ring-level failure. Ring memory model lives in hash_iouring_linux_ring.go.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"syscall"
)

// maxSingleRead caps the file size for a single IORING_OP_READ; larger files fall back to hashFile.
const maxSingleRead = 4 << 20

// iouringWindowFiles/Bytes cap concurrent FDs and buffer to avoid ulimit/heap exhaustion.
const (
	iouringWindowFiles = 256
	iouringWindowBytes = 64 << 20 // 64 MiB total buffer per window
)

// iouringHashBatch hashes files via io_uring IORING_OP_READ in sliding windows.
// Skips files >maxSingleRead (returns "" for caller fallback). Returns (nil, err) on ring failure.
func iouringHashBatch(files []relAbs) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}

	const ringSize = 64
	ring, err := NewRing(ringSize)
	if err != nil {
		return nil, err
	}
	defer ring.Close()

	results := make([]string, len(files))

	for winStart := 0; winStart < len(files); winStart += iouringWindowFiles {
		winEnd := winStart + iouringWindowFiles
		if winEnd > len(files) {
			winEnd = len(files)
		}
		window := files[winStart:winEnd]

		fds := make([]int, len(window))
		bufs := make([][]byte, len(window))
		for i := range fds {
			fds[i] = -1
		}
		var windowBytes int64
		for i, f := range window {
			info, err := os.Stat(f.abs)
			if err != nil || info.Size() > maxSingleRead || info.Size() == 0 {
				continue
			}
			if windowBytes+info.Size() > iouringWindowBytes {
				continue
			}
			fd, err := syscall.Open(f.abs, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
			if err != nil {
				continue
			}
			fds[i] = fd
			bufs[i] = make([]byte, info.Size())
			windowBytes += info.Size()
		}

		for batchStart := 0; batchStart < len(window); batchStart += ringSize {
			batchEnd := batchStart + ringSize
			if batchEnd > len(window) {
				batchEnd = len(window)
			}

			var submitted uint32
			for i := batchStart; i < batchEnd; i++ {
				if fds[i] < 0 || bufs[i] == nil {
					continue
				}
				if err := ring.SubmitRead(fds[i], bufs[i], uint64(winStart+i)); err != nil {
					continue
				}
				submitted++
			}
			if submitted == 0 {
				continue
			}

			if _, err := ring.SubmitAndWait(submitted); err != nil {
				for _, fd := range fds {
					if fd >= 0 {
						_ = syscall.Close(fd)
					}
				}
				return nil, err
			}
			ring.DrainCompletions(func(c CQE) {
				idx := int(c.UserData)
				if idx < 0 || idx >= len(files) {
					return
				}
				localIdx := idx - winStart
				if err := c.ReadErr(len(bufs[localIdx])); err != nil {
					return
				}
				h := sha256.Sum256(bufs[localIdx])
				results[idx] = hex.EncodeToString(h[:])
			})
		}

		for _, fd := range fds {
			if fd >= 0 {
				_ = syscall.Close(fd)
			}
		}
	}

	return results, nil
}

// iouringMinBatch is the minimum file count at which io_uring amortises its setup cost.
const iouringMinBatch = 16

// hashFilesIoUring is the Linux fast-path for hashFiles. Returns false on ring failure;
// caller falls through to the goroutine pool.
func (c *Cache) hashFilesIoUring(files []relAbs, hashes []string) bool {
	if len(files) < iouringMinBatch {
		return false
	}

	var toHash []relAbs
	toHashIdx := make([]int, 0, len(files))
	for i, f := range files {
		if hashes[i] != "" {
			continue
		}
		toHash = append(toHash, f)
		toHashIdx = append(toHashIdx, i)
	}
	if len(toHash) == 0 {
		return true
	}

	iuHashes, err := iouringHashBatch(toHash)
	if err != nil {
		return false
	}

	allDone := true
	for j, idx := range toHashIdx {
		if j < len(iuHashes) && iuHashes[j] != "" {
			hashes[idx] = iuHashes[j]
		} else {
			allDone = false
		}
	}
	return allDone
}
