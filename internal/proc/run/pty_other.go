//go:build !darwin && !linux

package run

import (
	"errors"
	"os"
	"syscall"
)

// ptySupported reports whether this platform can run a child on a terminal.
//
// False here, and deliberately not faked. Windows has ConPTY, which is a different
// API with different semantics, and wasm has no processes at all. A TTY request is
// refused with a clear error rather than silently downgraded to pipes: silently
// giving a caller the non-TTY behaviour they explicitly asked to avoid is the kind
// of quiet wrong answer that costs an afternoon to track down.
const ptySupported = false

var errNoPTY = errors.New("tty: not supported on this platform (unix only; Windows ConPTY is unimplemented)")

func openPTY(cols, rows int) (*os.File, *os.File, error) { return nil, nil, errNoPTY }

func ptySysProcAttr() *syscall.SysProcAttr { return nil }

func ptySize() (int, int) { return 80, 24 }
