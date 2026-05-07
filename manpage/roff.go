// Package manpage writes the groff_man(7) subset that magus's man pages use.
// Writer emits the macros; the Escape* helpers handle roff special characters.
// It is a standalone package free of magus-specific types, so anyone can import
// it to assemble man pages.
package manpage

import (
	"fmt"
	"io"
	"strings"
)

// Writer emits groff_man(7) macros to an io.Writer.
// Methods accept RAW roff strings; callers must Escape plain-text portions.
// Para is the exception: it escapes internally. B and I escape and wrap in \fB/\fI...\fR.
type Writer struct {
	w io.Writer
}

// NewWriter returns a Writer that emits groff_man(7) macros to w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// TH writes the title header.
func (w *Writer) TH(name, section, date, source, manual string) {
	fmt.Fprintf(w.w, ".TH %s %s %q %q %q\n", strings.ToUpper(name), section, date, source, manual)
}

// SH writes a section heading (uppercased per groff convention).
func (w *Writer) SH(title string) {
	fmt.Fprintf(w.w, ".SH %s\n", strings.ToUpper(title))
}

// SS writes a sub-section heading (case preserved).
func (w *Writer) SS(title string) {
	fmt.Fprintf(w.w, ".SS %s\n", title)
}

// P writes a paragraph break.
func (w *Writer) P() {
	fmt.Fprintln(w.w, ".PP")
}

// Para writes a plain-text paragraph, escaping special chars and splitting
// on blank lines with .PP between them. Use for Long / description fields.
func (w *Writer) Para(text string) {
	parts := SplitParas(text)
	for i, p := range parts {
		if i > 0 {
			w.P()
		}
		fmt.Fprintln(w.w, Escape(p))
	}
}

// TP writes a tagged paragraph (.TP); label and body are raw roff.
func (w *Writer) TP(label, body string) {
	fmt.Fprintln(w.w, ".TP")
	fmt.Fprintln(w.w, label)
	fmt.Fprintln(w.w, body)
}

// Indent begins an indented block (.RS).
func (w *Writer) Indent() {
	fmt.Fprintln(w.w, ".RS")
}

// Dedent ends an indented block (.RE).
func (w *Writer) Dedent() {
	fmt.Fprintln(w.w, ".RE")
}

// Example wraps lines in no-fill mode (.EX / .EE).
func (w *Writer) Example(lines ...string) {
	fmt.Fprintln(w.w, ".EX")
	for _, l := range lines {
		fmt.Fprintln(w.w, EscapeExample(l))
	}
	fmt.Fprintln(w.w, ".EE")
}

// B wraps text in bold roff sequences. text is plain text and is escaped.
func (*Writer) B(text string) string {
	return `\fB` + Escape(text) + `\fR`
}

// I wraps text in italic roff sequences. text is plain text and is escaped.
func (*Writer) I(text string) string {
	return `\fI` + Escape(text) + `\fR`
}

// Escape replaces roff special characters in plain text for correct man-page rendering.
func Escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\(rs`) // must come first to avoid double-escaping
	s = strings.ReplaceAll(s, "-", `\-`)   // roff minus sign (not a soft-hyphen break)
	if len(s) > 0 && (s[0] == '.' || s[0] == '\'') {
		s = `\&` + s // leading dot/apostrophe would be interpreted as a macro
	}
	return s
}

// EscapeExample is like Escape but keeps hyphens as literal '-' for copy-paste from pagers.
func EscapeExample(s string) string {
	s = strings.ReplaceAll(s, `\`, `\(rs`)
	if len(s) > 0 && (s[0] == '.' || s[0] == '\'') {
		s = `\&` + s
	}
	return s
}

// EscapeHyphen replaces literal '-' with roff '\-'.
func EscapeHyphen(s string) string {
	return strings.ReplaceAll(s, "-", `\-`)
}

// SplitParas splits text on blank lines, returning trimmed paragraphs.
func SplitParas(text string) []string {
	var paras []string
	var cur strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			if cur.Len() > 0 {
				paras = append(paras, strings.TrimSpace(cur.String()))
				cur.Reset()
			}
		} else {
			if cur.Len() > 0 {
				cur.WriteByte('\n')
			}
			cur.WriteString(line)
		}
	}
	if cur.Len() > 0 {
		paras = append(paras, strings.TrimSpace(cur.String()))
	}
	return paras
}
