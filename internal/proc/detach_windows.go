//go:build windows

package proc

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// DetachedSysProcAttr is the Windows counterpart to the unix Setsid detach. Windows has no
// session concept; CREATE_NEW_PROCESS_GROUP detaches the child from the parent console's
// Ctrl+C/Ctrl+Break group so a shell closing does not take the process down with it.
func DetachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
}

// RedirectStdio points os.Stdout and os.Stderr at path, appending. Windows delivers no
// SIGHUP, so it is called once, and the process that spawned this one already pointed
// its standard handles at the same file.
func RedirectStdio(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create log directory for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log %s: %w", path, err)
	}
	os.Stdout, os.Stderr = f, f
	return nil
}
