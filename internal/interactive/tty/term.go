package tty

import (
	"os"

	"golang.org/x/term"
)

// StdinIsTerminal reports whether standard input is connected to a terminal,
// rather than a pipe, a regular file, or /dev/null. Callers use it to fail fast
// with a clear message instead of blocking on a read of stdin that will never see
// piped input.
func StdinIsTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
