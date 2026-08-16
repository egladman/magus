//go:build !darwin && !linux

package main

import (
	"errors"
	"os"
	"syscall"
)

// Recording drives a pty, which this file's platforms do not give us through the
// same two calls darwin and linux do. The package still COMPILES everywhere -
// `go build ./...` and every lint pass cover the whole module on whatever host
// runs them, so excluding the package instead would trade this honest error for
// a build constraints exclude all Go files failure.
func ptsname(*os.File) (string, error) {
	return "", errors.New("magus-asciicast: recording needs a pty; supported on darwin and linux")
}

func sysProcAttr() *syscall.SysProcAttr { return nil }
