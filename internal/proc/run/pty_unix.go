//go:build darwin || linux

package run

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// ptySupported reports whether this platform can run a child on a terminal.
const ptySupported = true

// openPTY allocates a master/slave pair sized to the caller's geometry. The child
// is given the SLAVE, which is indistinguishable from a real terminal, so isatty
// returns true and the tool draws its interactive output. The parent reads the
// MASTER, which is where that output (escape sequences and all) arrives.
func openPTY(cols, rows int) (master *os.File, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}
	name, err := ptsname(master)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	// O_NOCTTY: the PARENT must not adopt this as its controlling terminal. The
	// child claims it instead, via Setctty in ptySysProcAttr below.
	slave, err = os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("open %s: %w", name, err)
	}
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ,
		&unix.Winsize{Row: uint16(rows), Col: uint16(cols)}); err != nil {
		slave.Close()
		master.Close()
		return nil, nil, fmt.Errorf("set winsize: %w", err)
	}
	return master, slave, nil
}

// ptySysProcAttr puts the child in a new session with the pty as its controlling
// terminal. Without Setctty the child has a terminal on its file descriptors but
// no CONTROLLING terminal, and anything using job control or reading /dev/tty
// misbehaves.
func ptySysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Setctty: true}
}

// ptySize reports the geometry to give the child: this process's own terminal when
// it has one, else a conventional default. Inheriting matters because tools lay
// their output out to the width they are told, and a child rendering for 80 columns
// inside a 200-column window looks broken.
func ptySize() (cols, rows int) {
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		if c, r, err := term.GetSize(int(f.Fd())); err == nil && c > 0 && r > 0 {
			return c, r
		}
	}
	return 80, 24
}
