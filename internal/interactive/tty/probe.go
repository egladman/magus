// Package tty provides terminal-agnostic primitives: scroll regions,
// file-descriptor classification (TTY vs pipe vs regular file), and the
// picker. Terminal status and dimensions are read through the Probe
// interface, which callers inject, so tests can answer both without
// opening a pty.
package tty

import (
	"io"
	"os"

	"golang.org/x/term"
)

// Probe answers the two questions a caller needs about a file
// descriptor before drawing to it: whether it is a terminal the user is
// typing at (rather than a pipe, regular file, or /dev/null) and how
// large that terminal is.
//
// The methods take a descriptor rather than an io.Writer so the
// interface stays independent of io and serves stdin, stdout, and
// stderr alike; callers resolve the descriptor once with [Fd].
type Probe interface {
	IsTerminal(fd uintptr) bool
	Size(fd uintptr) (width, height int, err error)
}

// systemProbe asks the operating system, via golang.org/x/term, for
// terminal status and dimensions.
type systemProbe struct{}

func (systemProbe) IsTerminal(fd uintptr) bool { return term.IsTerminal(int(fd)) }

func (systemProbe) Size(fd uintptr) (width, height int, err error) {
	return term.GetSize(int(fd))
}

// SystemProbe is the production Probe: it asks the operating system.
// It is stateless and safe for concurrent use, so it is a value rather
// than something a constructor hands out. Tests inject their own Probe
// instead of replacing this.
var SystemProbe Probe = systemProbe{}

// Fd returns the file descriptor backing w, and whether w has one at
// all. A bytes.Buffer, an io.Pipe writer, and a network connection all
// report false.
//
// The boolean is not decoration: descriptor 0 is stdin, a perfectly
// real terminal, so a lone uintptr has no spare value to mean "none".
// Returning the two separately keeps a caller from probing stdin when
// it meant to ask about a buffer.
func Fd(w io.Writer) (uintptr, bool) {
	f, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return 0, false
	}
	return f.Fd(), true
}

// StdinIsTerminal reports whether standard input is a terminal the user
// is typing at, rather than a pipe, a file, or /dev/null. Callers use it
// to fail fast with a clear message instead of blocking on a read of
// stdin that will never see input.
func StdinIsTerminal() bool { return SystemProbe.IsTerminal(os.Stdin.Fd()) }

// IsTerminalWriter reports whether w is a terminal according to p. It is
// the writer-shaped form of Probe.IsTerminal: a writer with no descriptor
// is never a terminal, which is the check every caller would otherwise
// hand-roll around [Fd].
func IsTerminalWriter(w io.Writer, p Probe) bool {
	fd, ok := Fd(w)
	return ok && p.IsTerminal(fd)
}

// WantsColor reports whether output written to w should carry ANSI
// colour: w must be a terminal, and NO_COLOR must be unset.
//
// This is one question with one answer, so it lives in one place. Before
// this existed, the cache's log handler, the doctor command, and the
// status grid each decided it independently, and only two of the three
// honoured NO_COLOR.
//
// See https://no-color.org: any non-empty value disables colour.
func WantsColor(w io.Writer, p Probe) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return IsTerminalWriter(w, p)
}
