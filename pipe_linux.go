//go:build linux

package magus

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// dupForDrain duplicates stdin for the drain to read, non-blocking so closing it
// interrupts a pending read. The flag lives on the open file description stdin shares,
// so restore puts it back for a stdin that is then used as it was.
//
// This file and pipe_darwin.go carry the same relay; keep the two in step.
func dupForDrain(stdin *os.File) (*os.File, func() error, error) {
	fd := int(stdin.Fd())
	dup, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	if err := unix.SetNonblock(dup, true); err != nil {
		_ = unix.Close(dup)
		return nil, nil, err
	}
	restore := func() error { return unix.SetNonblock(fd, false) }
	return os.NewFile(uintptr(dup), stdin.Name()), restore, nil
}

// installRelay puts the read end of a fresh pipe at stdin's descriptor and returns the
// write end. Both are blocking, as a stdin that was never polled expects, and the write
// end is close-on-exec so no child keeps the relay open past this process.
func installRelay(stdin *os.File) (*os.File, error) {
	var p [2]int
	syscall.ForkLock.RLock()
	err := unix.Pipe(p[:])
	if err == nil {
		unix.CloseOnExec(p[0])
		unix.CloseOnExec(p[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, err
	}
	if err := unix.Dup2(p[0], int(stdin.Fd())); err != nil {
		_ = unix.Close(p[0])
		_ = unix.Close(p[1])
		return nil, err
	}
	_ = unix.Close(p[0])
	return os.NewFile(uintptr(p[1]), "stdin relay"), nil
}
