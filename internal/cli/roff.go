// writer (this file) emits the groff_man(7) subset that magus's man pages use;
// the escape* helpers handle roff special characters. Free of magus-specific types.

package cli

import (
	"fmt"
	"io"
	"strings"
)

// writer emits groff_man(7) macros to an io.Writer.
// Methods accept RAW roff strings; callers must escape plain-text portions.
// Para is the exception: it escapes internally. B and I escape and wrap in \fB/\fI...\fR.
type writer struct {
	w io.Writer
}

// newWriter returns a writer that emits groff_man(7) macros to w.
func newWriter(w io.Writer) *writer { return &writer{w: w} }

// TH writes the title header.
func (w *writer) TH(name, section, date, source, manual string) {
	fmt.Fprintf(w.w, ".TH %s %s %q %q %q\n", strings.ToUpper(name), section, date, source, manual)
}

// SH writes a section heading (uppercased per groff convention).
func (w *writer) SH(title string) {
	fmt.Fprintf(w.w, ".SH %s\n", strings.ToUpper(title))
}

// SS writes a sub-section heading (case preserved).
func (w *writer) SS(title string) {
	fmt.Fprintf(w.w, ".SS %s\n", title)
}

// P writes a paragraph break.
func (w *writer) P() {
	fmt.Fprintln(w.w, ".PP")
}

// Para writes a plain-text paragraph, escaping special chars and splitting
// on blank lines with .PP between them. Use for Long / description fields.
func (w *writer) Para(text string) {
	parts := SplitParas(text)
	for i, p := range parts {
		if i > 0 {
			w.P()
		}
		fmt.Fprintln(w.w, escape(p))
	}
}

// TP writes a tagged paragraph (.TP); label and body are raw roff.
func (w *writer) TP(label, body string) {
	fmt.Fprintln(w.w, ".TP")
	fmt.Fprintln(w.w, label)
	fmt.Fprintln(w.w, body)
}

// Indent begins an indented block (.RS).
func (w *writer) Indent() {
	fmt.Fprintln(w.w, ".RS")
}

// Dedent ends an indented block (.RE).
func (w *writer) Dedent() {
	fmt.Fprintln(w.w, ".RE")
}

// Example wraps lines in no-fill mode (.EX / .EE).
func (w *writer) Example(lines ...string) {
	fmt.Fprintln(w.w, ".EX")
	for _, l := range lines {
		fmt.Fprintln(w.w, escapeExample(l))
	}
	fmt.Fprintln(w.w, ".EE")
}

// B wraps text in bold roff sequences. text is plain text and is escaped.
func (*writer) B(text string) string {
	return `\fB` + escape(text) + `\fR`
}

// escape replaces roff special characters in plain text for correct man-page rendering.
func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\(rs`) // must come first to avoid double-escaping
	s = strings.ReplaceAll(s, "-", `\-`)   // roff minus sign (not a soft-hyphen break)
	if len(s) > 0 && (s[0] == '.' || s[0] == '\'') {
		s = `\&` + s // leading dot/apostrophe would be interpreted as a macro
	}
	return s
}

// escapeExample is like escape but keeps hyphens as literal '-' for copy-paste from pagers.
func escapeExample(s string) string {
	s = strings.ReplaceAll(s, `\`, `\(rs`)
	if len(s) > 0 && (s[0] == '.' || s[0] == '\'') {
		s = `\&` + s
	}
	return s
}

// escapeHyphen replaces literal '-' with roff '\-'.
func escapeHyphen(s string) string {
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
