//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// redirectStdio points os.Stdout and os.Stderr at path, appending. Windows delivers no
// SIGHUP, so it is called once, and the process that spawned this one already pointed
// its standard handles at the same file.
func redirectStdio(path string) error {
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
