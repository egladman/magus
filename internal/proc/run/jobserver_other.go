//go:build windows || wasm

package run

import (
	"errors"
	"os"
)

// GNU make on windows shares slots through a named semaphore (--jobserver-auth=NAME),
// not a pipe, and exec.Cmd.ExtraFiles is unsupported there; wasm runs no processes.
func openJobserverPipe() (r, w *os.File, err error) { return nil, nil, errors.ErrUnsupported }
