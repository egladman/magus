package generate

import (
	"bytes"
	"testing"
)

// emit runs fn against a fresh writer over a buffer and returns the emitted text.
// Every writer method is exercised through this helper so the golden strings
// below assert the exact bytes a man page would receive.
func emit(fn func(*writer)) string {
	var buf bytes.Buffer
	fn(newWriter(&buf))
	return buf.String()
}

func TestWriterTH(t *testing.T) {
	// TH uppercases the name and %q-quotes the last three fields (date/source/manual).
	got := emit(func(w *writer) {
		w.TH("magus", "1", "2026-07-08", "magus 0.1", "Magus Manual")
	})
	equal(t, ".TH MAGUS 1 \"2026-07-08\" \"magus 0.1\" \"Magus Manual\"\n", got)
}

func TestWriterSH(t *testing.T) {
	// SH uppercases the section title per groff convention.
	got := emit(func(w *writer) { w.SH("description") })
	equal(t, ".SH DESCRIPTION\n", got)
}

func TestWriterSS(t *testing.T) {
	// SS preserves case for sub-section headings.
	got := emit(func(w *writer) { w.SS("Sub Heading") })
	equal(t, ".SS Sub Heading\n", got)
}

func TestWriterP(t *testing.T) {
	got := emit(func(w *writer) { w.P() })
	equal(t, ".PP\n", got)
}

func TestWriterPara(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			// Single paragraph: escaped, no .PP prefix.
			name: "single",
			in:   "hello world",
			want: "hello world\n",
		},
		{
			// Blank-line separated paragraphs get a .PP break between them.
			name: "two paras",
			in:   "first para\n\nsecond para",
			want: "first para\n.PP\nsecond para\n",
		},
		{
			// Escaping applies per paragraph: the hyphen becomes a roff minus.
			name: "escapes hyphen",
			in:   "dry-run",
			want: "dry\\-run\n",
		},
		{
			// Empty input yields no paragraphs, so nothing is written.
			name: "empty",
			in:   "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := emit(func(w *writer) { w.Para(tt.in) })
			equal(t, tt.want, got)
		})
	}
}

func TestWriterTP(t *testing.T) {
	// TP emits the .TP macro, then the raw label and body on their own lines.
	got := emit(func(w *writer) { w.TP("\\fB\\-\\-flag\\fR", "flag help") })
	equal(t, ".TP\n\\fB\\-\\-flag\\fR\nflag help\n", got)
}

func TestWriterIndentDedent(t *testing.T) {
	got := emit(func(w *writer) {
		w.Indent()
		w.Dedent()
	})
	equal(t, ".RS\n.RE\n", got)
}

func TestWriterExample(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{
			// No lines still emits the .EX/.EE fences.
			name:  "empty",
			lines: nil,
			want:  ".EX\n.EE\n",
		},
		{
			// Example uses escapeExample: hyphens stay literal for copy-paste.
			name:  "keeps hyphens literal",
			lines: []string{"magus run build --dry-run"},
			want:  ".EX\nmagus run build --dry-run\n.EE\n",
		},
		{
			// A leading dot is guarded with \& so it is not read as a macro.
			name:  "guards leading dot",
			lines: []string{".hidden line"},
			want:  ".EX\n\\&.hidden line\n.EE\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := emit(func(w *writer) { w.Example(tt.lines...) })
			equal(t, tt.want, got)
		})
	}
}

func TestWriterB(t *testing.T) {
	// B escapes the text and wraps it in \fB...\fR. Note the returned string is
	// not written to the buffer; B is a pure helper.
	w := newWriter(&bytes.Buffer{})
	equal(t, "\\fBbuild\\fR", w.B("build"))
	// The hyphen inside is escaped to a roff minus.
	equal(t, "\\fB\\-\\-flag\\fR", w.B("--flag"))
}

func TestEscape(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "hello", want: "hello"},
		{
			// Backslash is rewritten to \(rs; it must run first so the escapes it
			// introduces are not themselves re-escaped.
			name: "backslash",
			in:   "a\\b",
			want: "a\\(rsb",
		},
		{name: "hyphen", in: "a-b", want: "a\\-b"},
		{
			// A leading dot would be parsed as a macro, so it is guarded with \&.
			name: "leading dot",
			in:   ".config",
			want: "\\&.config",
		},
		{
			// A leading apostrophe is likewise a control character.
			name: "leading apostrophe",
			in:   "'quoted",
			want: "\\&'quoted",
		},
		{
			// A dot only matters in the first column; interior dots are untouched.
			name: "interior dot",
			in:   "a.b",
			want: "a.b",
		},
		{name: "empty", in: "", want: ""},
		{
			// Combined: leading dot plus a backslash and a hyphen.
			name: "combined",
			in:   ".a\\-",
			want: "\\&.a\\(rs\\-",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			equal(t, tt.want, escape(tt.in))
		})
	}
}

func TestEscapeExample(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "hello", want: "hello"},
		// Unlike escape, hyphens are preserved literally for copy-paste.
		{name: "keeps hyphen", in: "a-b", want: "a-b"},
		{name: "backslash", in: "a\\b", want: "a\\(rsb"},
		{name: "leading dot", in: ".x", want: "\\&.x"},
		{name: "leading apostrophe", in: "'x", want: "\\&'x"},
		{name: "empty", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			equal(t, tt.want, escapeExample(tt.in))
		})
	}
}

func TestEscapeHyphen(t *testing.T) {
	equal(t, "\\-", escapeHyphen("-"))
	equal(t, "a\\-b\\-c", escapeHyphen("a-b-c"))
	equal(t, "none", escapeHyphen("none"))
	equal(t, "", escapeHyphen(""))
}

func TestSplitParagraphs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty", in: "", want: nil},
		{name: "single line", in: "one line", want: []string{"one line"}},
		{
			// Lines within a paragraph are joined by a newline and trimmed.
			name: "multiline single para",
			in:   "line one\nline two",
			want: []string{"line one\nline two"},
		},
		{
			// A blank line separates paragraphs.
			name: "two paras",
			in:   "para one\n\npara two",
			want: []string{"para one", "para two"},
		},
		{
			// Multiple consecutive blank lines collapse to one break, and
			// surrounding whitespace is trimmed from each paragraph.
			name: "extra blank lines and whitespace",
			in:   "\n\n  first  \n\n\n  second  \n\n",
			want: []string{"first", "second"},
		},
		{
			// A line that is only whitespace counts as blank.
			name: "whitespace-only line separates",
			in:   "a\n   \nb",
			want: []string{"a", "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			equal(t, tt.want, splitParas(tt.in))
		})
	}
}
