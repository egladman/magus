//go:build !linux

package audit

import (
	"errors"
	"io/fs"
	"os"
)

type dirEntKind uint8

const (
	dentSkip dirEntKind = iota
	dentDir
	dentRegular
)

// readDirEnts is the portable fallback. See walk_linux.go for the
// contract. This path does not avoid the *DirEntry per-entry allocation
// or the readdir slice grow; the win is gated on getdents64 availability.
func readDirEnts(dirPath string, fn func(name []byte, kind dirEntKind)) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, de := range entries {
		var kind dirEntKind
		switch {
		case de.IsDir():
			kind = dentDir
		case de.Type().IsRegular():
			kind = dentRegular
		default:
			continue
		}
		// os.DirEntry.Name() returns a string; convert to []byte without
		// copying via unsafe is tempting but we'd violate fs.DirEntry's
		// implicit immutability contract. A copy is fine on the fallback
		// path; this branch isn't the perf hot path.
		fn([]byte(de.Name()), kind)
	}
	return nil
}
