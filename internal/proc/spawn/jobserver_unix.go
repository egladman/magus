//go:build !windows && !wasm

package spawn

import "os"

func openJobserverPipe() (r, w *os.File, err error) { return os.Pipe() }
