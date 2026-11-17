//go:build !windows

package proc

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// DetachedSysProcAttr detaches a spawned broker or server from the launching shell.
// Setsid puts the child in its own session with no controlling terminal, so it survives
// the parent process exiting and a terminal hangup: the same isolation `setsid` or a
// process supervisor would give it.
func DetachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// RedirectStdio points this process's stdout and stderr at path, appending, so everything
// it writes lands there: slog, a line to os.Stderr, a runtime panic. Called again it
// reopens path, which is how a log rotator that moved the file aside gets a fresh one
// without a restart. The descriptors are replaced in place (dup2), so nothing holding
// os.Stderr needs telling.
func RedirectStdio(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create log directory for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	fd := int(f.Fd())
	for _, std := range []int{1, 2} {
		if err := unix.Dup2(fd, std); err != nil {
			return fmt.Errorf("point fd %d at %s: %w", std, path, err)
		}
	}
	return nil
}
