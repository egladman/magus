package termtest

import (
	"bytes"
	"strings"
)

// Escape sequences tests look for in raw output.
const (
	EL           = "\x1b[K"
	EL2          = "\x1b[2K"
	CursorSave   = "\x1b7"
	DECSTBMReset = "\x1b[r"
)

// Buffer is a bytes.Buffer with a synthetic descriptor, so a Probe that says
// "terminal" is believed without a pty.
type Buffer struct{ bytes.Buffer }

// Fd returns a fixed descriptor.
func (*Buffer) Fd() uintptr { return 2 }

// Probe is a Probe whose answers the test sets.
type Probe struct {
	TTY           bool
	Width, Height int
	SizeErr       error
}

// IsTerminal reports the preset answer.
func (p Probe) IsTerminal(uintptr) bool { return p.TTY }

// Size reports the preset size.
func (p Probe) Size(uintptr) (int, int, error) { return p.Width, p.Height, p.SizeErr }

// Terminal is a Probe for a terminal of the given size.
func Terminal(width, height int) Probe { return Probe{TTY: true, Width: width, Height: height} }

// NotATerminal is a Probe that calls every descriptor a pipe.
func NotATerminal() Probe { return Probe{} }

// StripANSI drops CSI and two-byte escapes, leaving the visible text.
func StripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) {
			if s[i+1] == '[' {
				j := i + 2
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) {
					j++
				}
				i = j + 1
				continue
			}
			i += 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
