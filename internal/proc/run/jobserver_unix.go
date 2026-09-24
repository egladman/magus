//go:build !windows && !wasm

package run

import "os"

func openJobserverPipe() (r, w *os.File, err error) { return os.Pipe() }
