package cells

import (
	"strings"
	"unicode/utf8"
)

// Ellipsis marks a clipped line. ASCII, because it lands in terminal output.
const Ellipsis = "..."

const (
	sgrReset   = "\x1b[0m"
	osc8Prefix = "\x1b]8;;"
	osc8End    = "\x1b]8;;\x1b\\"
)

// EscapeLen reports the length of the escape sequence starting at s, or 0 when
// s does not start with one. It knows CSI and OSC, so a hyperlink's URI never
// counts as visible text.
func EscapeLen(s string) int {
	if len(s) < 2 || s[0] != 0x1b {
		return 0
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']':
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	}
	// A two-byte escape (ESC 7, ESC 8, ESC D).
	return 2
}

// Cols is the display width of s, escape sequences excluded. A tree branch is
// three bytes and one column, so len() is not this.
func Cols(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if e := EscapeLen(s[i:]); e > 0 {
			i += e
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	return n
}

// Clip shortens s to cols display columns, never cutting inside an escape
// sequence. A cut closes any style or hyperlink still open.
func Clip(s string, cols int) string {
	if cols <= 0 {
		return ""
	}
	var b strings.Builder
	used, i := 0, 0
	for i < len(s) && used < cols {
		if n := EscapeLen(s[i:]); n > 0 {
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		used++
		i += size
	}
	// Closers right after the last column come along. An opener would start
	// the cell that did not fit.
	for i < len(s) {
		n := EscapeLen(s[i:])
		if n == 0 || !closesStyle(s[i:i+n]) {
			break
		}
		b.WriteString(s[i : i+n])
		i += n
	}
	out := b.String()
	if i >= len(s) {
		return out
	}
	if strings.Count(out, osc8Prefix)-strings.Count(out, osc8End) > 0 {
		out += osc8End
	}
	if strings.Contains(out, "\x1b[") && !strings.HasSuffix(out, sgrReset) {
		out += sgrReset
	}
	return out
}

func closesStyle(seq string) bool {
	return seq == sgrReset || seq == "\x1b[m" || seq == osc8End
}

// Fit shortens text to width display columns, ending in an ellipsis when it
// cut, so a reader never takes a half value for the whole one.
func Fit(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if Cols(text) <= width {
		return text
	}
	if width <= Cols(Ellipsis) {
		return Ellipsis[:width]
	}
	return Clip(text, width-Cols(Ellipsis)) + Ellipsis
}

// ClipBytes shortens msg to nBytes, ellipsis included. A rune straddling the
// cut is dropped whole. Styled text needs [Clip] instead.
func ClipBytes(msg string, nBytes int) string {
	if nBytes <= 0 {
		return ""
	}
	if len(msg) <= nBytes {
		return msg
	}
	if nBytes <= len(Ellipsis) {
		return Ellipsis[:nBytes]
	}
	cut := nBytes - len(Ellipsis)
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}
	return msg[:cut] + Ellipsis
}
