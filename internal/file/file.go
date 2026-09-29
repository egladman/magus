// Package file provides filesystem primitives used across the magus module.
package file

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path via a temp file in path's directory, renamed into
// place, creating the directory when absent. Readers see the old file or the new one,
// never a partial one. The contents are fsync'd before the rename, so a crash cannot
// leave the new name over unflushed bytes; the directory is not fsync'd, so a crash can
// still lose the rename itself and leave the previous file in place.
//
// The file ends up with mode perm. The temp file is a dotfile, so a glob over the
// directory never picks it up, and it is removed on every failure, the rename included.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(path, data, perm, true)
}

// ReplaceFile is WriteFileAtomic without the fsync. Readers still never see a partial
// file, but a crash can lose the write or leave path empty. It is for records that are
// cheaper to lose than to flush: on macOS the fsync is F_FULLFSYNC, about 5ms a file.
func ReplaceFile(path string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(path, data, perm, false)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode, sync bool) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Same directory, because rename is atomic only within one filesystem and
	// os.TempDir is routinely another mount.
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if sync {
		if err := tmp.Sync(); err != nil {
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp makes the file 0600, and the rename would carry that through.
	if err := Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
