package tty

import (
	"fmt"

	"golang.org/x/term"
)

// MakeRaw switches the terminal behind fd into raw mode, so a reader
// receives each keystroke as it is typed rather than a line at a time,
// and returns the function that puts the terminal back.
//
// Restoring is not optional: a process that exits from raw mode leaves
// the user's shell without echo, which looks like a hung terminal.
// Callers defer the returned function on every path.
//
// It is returned as a closure rather than an opaque state value so a
// caller cannot restore the wrong descriptor, and so the "how" of
// restoring lives here instead of at each call site.
func MakeRaw(fd uintptr) (restore func() error, err error) {
	state, err := term.MakeRaw(int(fd))
	if err != nil {
		return nil, fmt.Errorf("tty: enter raw mode: %w", err)
	}
	return func() error {
		if err := term.Restore(int(fd), state); err != nil {
			return fmt.Errorf("tty: restore terminal: %w", err)
		}
		return nil
	}, nil
}
