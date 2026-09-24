// Package file provides filesystem primitives used across the magus module.
package file

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path via a same-directory temp file + rename.
// The temp file is fsync'd before rename; readers never see a partial file.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(path, data, perm, true)
}

// ReplaceFile is WriteFileAtomic without the fsync. Readers still never see a partial
// file, but a crash can lose the write or leave path empty. It is for records that are
// cheaper to lose than to flush: on macOS the fsync is F_FULLFSYNC, about 5ms a file.
func ReplaceFile(path string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(path, data, perm, false)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode, sync bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if sync {
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := Chmod(tmpName, perm); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
