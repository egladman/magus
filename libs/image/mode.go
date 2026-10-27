package image

import (
	"fmt"
	"io/fs"
)

// Mode is a file permission (permission, setuid, setgid, sticky bits only).
type Mode fs.FileMode

// Named modes for Spec placements. Values are the Unix permission bits.
const (
	ModeFile       Mode = 0o644
	ModeExecutable Mode = 0o755
	ModePrivate    Mode = 0o600
	ModeReadonly   Mode = 0o444
)

// NewMode accepts a permission value and rejects type bits.
func NewMode(bits int) (Mode, error) {
	if bits < 0 || bits > 0o7777 {
		return 0, fmt.Errorf("image: mode %#o: only permission, setuid, setgid and sticky bits are allowed", bits)
	}
	m := Mode(bits)
	if fs.FileMode(m)&^0o7777 != 0 {
		return 0, fmt.Errorf("image: mode %#o: only permission, setuid, setgid and sticky bits are allowed", bits)
	}
	return m, nil
}

func (m Mode) FileMode() fs.FileMode { return fs.FileMode(m) }
