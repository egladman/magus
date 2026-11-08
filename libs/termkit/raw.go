package termkit

import (
	"fmt"

	"golang.org/x/term"
)

// RawMode is a terminal switched into raw mode by [MakeRaw]. It remembers the
// descriptor, so a caller cannot restore the wrong one.
type RawMode struct {
	fd    uintptr
	state *term.State
}

// MakeRaw switches the terminal behind fd into raw mode, so a reader receives
// each keystroke as it is typed rather than a line at a time.
//
// Restoring is not optional: a process that exits from raw mode leaves the
// user's shell without echo. Callers defer [RawMode.Restore] on every path.
func MakeRaw(fd uintptr) (*RawMode, error) {
	state, err := term.MakeRaw(int(fd))
	if err != nil {
		return nil, fmt.Errorf("tty: enter raw mode: %w", err)
	}
	return &RawMode{fd: fd, state: state}, nil
}

// Restore puts the terminal back the way MakeRaw found it.
func (r *RawMode) Restore() error {
	if err := term.Restore(int(r.fd), r.state); err != nil {
		return fmt.Errorf("tty: restore terminal: %w", err)
	}
	return nil
}
