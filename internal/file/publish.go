package file

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Publish writes data to path only when path does not exist, and reports [fs.ErrExist]
// when it does, leaving that file as it was. A reader sees no file or the whole of data,
// never an empty or partial one. The bytes are synced before they appear. The directory
// must exist: Publish does not create it, so the caller owns its mode.
//
// Creating path with O_EXCL and then writing it is the obvious alternative and is wrong:
// the file exists empty until the write lands, and a reader in that window sees "".
func Publish(path string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), fmt.Sprintf(".%s.tmp.*", filepath.Base(path)))
	if err != nil {
		return err
	}
	// The link publishes; the temp is always litter.
	defer func() { err = errors.Join(err, os.Remove(tmp.Name())) }()
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	// A link, not a rename: a rename replaces whatever holds path, and a link fails when
	// path exists, so it is the uniqueness check and the publication in one step.
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("file: publish %s: %w", path, fs.ErrExist)
		}
		return err
	}
	return nil
}
