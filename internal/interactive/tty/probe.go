// Package tty provides terminal-agnostic primitives: scroll regions,
// file-descriptor classification (TTY vs pipe vs regular file), and the
// picker. Terminal status and dimensions are read through the Probe
// interface, which callers inject, so tests can answer both without
// opening a pty.
package tty

import (
	"io"
	"os"
	"strings"

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

// IsTerminalReader reports whether r is a terminal the user is typing at,
// rather than a pipe, a file, or /dev/null. Callers use it to fail fast with a
// clear message instead of blocking on a read that will never see input.
//
// Takes a Probe like every other predicate here, so a test can answer it
// without a pty. It was the only one that could not be redirected.
func IsTerminalReader(r *os.File, p Probe) bool {
	fd, ok := Fd(r)
	return ok && p.IsTerminal(fd)
}

// StdinIsTerminal is [IsTerminalReader] for the process's own standard input.
func StdinIsTerminal() bool { return IsTerminalReader(os.Stdin, SystemProbe) }

// IsTerminalWriter reports whether w is a terminal according to p. It is
// the writer-shaped form of Probe.IsTerminal: a writer with no descriptor
// is never a terminal, which is the check every caller would otherwise
// hand-roll around [Fd].
func IsTerminalWriter(w io.Writer, p Probe) bool {
	fd, ok := Fd(w)
	return ok && p.IsTerminal(fd)
}

// CanRender reports whether escape sequences may be written to w at all: it
// must be a terminal, and TERM must not declare one that understands none.
//
// TERM=dumb is not a hypothetical. Emacs shell-mode sets it, and the pty behind
// it IS a terminal - so a descriptor check alone says yes and the cursor
// addressing, scroll margins and colour all go out to something that will
// render them as literal garbage. That is the artifacting this whole package is
// supposed to be incapable of.
//
// This is the gate for anything that MOVES the cursor or reserves rows.
// [WantsColor] adds NO_COLOR on top for the narrower question of styling.
func CanRender(w io.Writer, p Probe) bool {
	return IsTerminalWriter(w, p) && os.Getenv("TERM") != "dumb"
}

// WantsHyperlinks reports whether OSC 8 hyperlinks may be written to w.
//
// Deliberately NOT gated on NO_COLOR: that variable is about colour, and a link
// is not colour - stripping it would take away a way to reach something rather
// than tone the output down. It IS gated on the two terminals known to render
// the sequence badly rather than swallow it:
//
//   - TERM=dumb, which by definition handles no escape at all.
//   - screen, whose OSC pass-through mangles the sequence and leaves the URI
//     visible in the output. tmux is fine and is not excluded.
//
// Everything else either honours OSC 8 or ignores an unknown OSC cleanly,
// which is what makes emitting it safe rather than a gamble on the reader's
// terminal.
func WantsHyperlinks(w io.Writer, p Probe) bool {
	if !CanRender(w, p) {
		return false
	}
	if strings.HasPrefix(os.Getenv("TERM"), "screen") {
		return false
	}
	// Over ssh the only links magus emits are file:// URLs naming paths on the
	// REMOTE machine, while the terminal that would open them resolves them
	// against the LOCAL filesystem. The link is not merely unhelpful there, it
	// is wrong: it either fails or, worse, opens an unrelated local file that
	// happens to share the path. OSC 8 can carry a hostname for exactly this,
	// but essentially no terminal fetches a remote file, so the honest answer
	// is to emit plain text and let the reader copy the command.
	return os.Getenv("SSH_TTY") == "" && os.Getenv("SSH_CONNECTION") == ""
}

// WantsColor reports whether output written to w should carry ANSI
// colour: w must be a terminal, and NO_COLOR must be unset.
//
// This is one question with one answer, so it lives in one place. Before
// this existed, the cache's log handler, the doctor command, and the
// status grid each decided it independently, and only two of the three
// honoured NO_COLOR.
//
// See https://no-color.org: any non-empty value disables colour. TERM=dumb
// disables it too, via [CanRender] - this function's documentation has always
// said so, and until now only the NO_COLOR half was true.
func WantsColor(w io.Writer, p Probe) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return CanRender(w, p)
}
