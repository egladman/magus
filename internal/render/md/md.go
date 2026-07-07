// Package md is a small typed Markdown builder for magus's generated docs
// (MAGUS.md, the insight report). It replaces hand-concatenated markdown with
// block-level primitives - headings, paragraphs, tables, code fences, lists -
// so table pipes, fence closing, and block spacing are written once here
// instead of at every call site. Every block method leaves exactly one blank
// line after itself, so blocks compose without callers tracking spacing.
//
// It is a builder, not a renderer: output goes wherever the caller writes it
// (emit, never render). Cell and label text is taken verbatim - inputs are
// sanitized at graph ingest, and generated docs deliberately embed inline
// markdown (backticks, bold) in cells.
package md

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// Align is a table column alignment, rendered as the GFM delimiter cell.
type Align int

const (
	Left   Align = iota // ---
	Right               // --:
	Center              // :-:
)

// delimiter returns the GFM delimiter-row cell for the alignment.
func (a Align) delimiter() string {
	switch a {
	case Right:
		return "--:"
	case Center:
		return ":-:"
	}
	return "---"
}

// Builder accumulates a Markdown document. The zero value is ready to use.
type Builder struct {
	buf bytes.Buffer
}

// Grow hints the final document size, like bytes.Buffer.Grow.
func (b *Builder) Grow(n int) { b.buf.Grow(n) }

// Heading writes an ATX heading at the given level (1-6).
func (b *Builder) Heading(level int, text string) {
	if level < 1 {
		level = 1
	}
	if level > 6 {
		level = 6
	}
	b.buf.WriteString(strings.Repeat("#", level))
	b.buf.WriteByte(' ')
	b.buf.WriteString(text)
	b.buf.WriteString("\n\n")
}

// Paragraph writes text as its own block.
func (b *Builder) Paragraph(text string) {
	b.buf.WriteString(text)
	b.buf.WriteString("\n\n")
}

// Paragraphf writes a formatted paragraph block.
func (b *Builder) Paragraphf(format string, args ...any) {
	fmt.Fprintf(&b.buf, format, args...)
	b.buf.WriteString("\n\n")
}

// Comment writes an HTML comment block (e.g. the "generated, do not edit" marker).
func (b *Builder) Comment(text string) {
	b.buf.WriteString("<!-- ")
	b.buf.WriteString(text)
	b.buf.WriteString(" -->\n\n")
}

// List writes a bullet list, one "- item" line per entry. No-op when empty.
func (b *Builder) List(items ...string) {
	if len(items) == 0 {
		return
	}
	for _, it := range items {
		b.buf.WriteString("- ")
		b.buf.WriteString(it)
		b.buf.WriteByte('\n')
	}
	b.buf.WriteByte('\n')
}

// Table writes a GFM table: a header row, the alignment delimiter row, then
// one row per entry. align may be nil (all Left) or shorter than header (the
// tail defaults to Left). Cells are written verbatim; callers pre-format
// values (and may embed inline code). No-op when there are no rows.
func (b *Builder) Table(header []string, align []Align, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	b.buf.WriteString("| ")
	b.buf.WriteString(strings.Join(header, " | "))
	b.buf.WriteString(" |\n|")
	for i := range header {
		var a Align
		if i < len(align) {
			a = align[i]
		}
		b.buf.WriteString(a.delimiter())
		b.buf.WriteByte('|')
	}
	b.buf.WriteByte('\n')
	for _, row := range rows {
		b.buf.WriteString("| ")
		b.buf.WriteString(strings.Join(row, " | "))
		b.buf.WriteString(" |\n")
	}
	b.buf.WriteByte('\n')
}

// CodeBlock writes a fenced code block with one line per entry.
func (b *Builder) CodeBlock(lang string, lines ...string) {
	b.openFence(lang)
	for _, l := range lines {
		b.buf.WriteString(l)
		b.buf.WriteByte('\n')
	}
	b.closeFence()
}

// AlignedCodeBlock writes a fenced code block of code lines with their
// trailing "# note" comments aligned into one column. A line with an empty
// note carries no comment.
func (b *Builder) AlignedCodeBlock(lang string, lines []CodeLine) {
	width := 0
	for _, l := range lines {
		if l.Note != "" && len(l.Code) > width {
			width = len(l.Code)
		}
	}
	b.openFence(lang)
	for _, l := range lines {
		if l.Note == "" {
			b.buf.WriteString(l.Code)
			b.buf.WriteByte('\n')
			continue
		}
		fmt.Fprintf(&b.buf, "%-*s  # %s\n", width, l.Code, l.Note)
	}
	b.closeFence()
}

// CodeLine is one line of an AlignedCodeBlock: the code and its comment.
type CodeLine struct{ Code, Note string }

// Fenced writes a fenced block whose body comes from emit (e.g. a Mermaid
// emitter that takes an io.Writer). The fence is closed even when emit fails,
// but the error is returned as-is.
func (b *Builder) Fenced(lang string, emit func(io.Writer) error) error {
	b.openFence(lang)
	err := emit(&b.buf)
	b.closeFence()
	return err
}

func (b *Builder) openFence(lang string) {
	b.buf.WriteString("```")
	b.buf.WriteString(lang)
	b.buf.WriteByte('\n')
}

func (b *Builder) closeFence() {
	b.buf.WriteString("```\n\n")
}

// Details writes a <details> disclosure block: the summary line, a blank
// line, then whatever body writes into the builder.
func (b *Builder) Details(summary string, body func(*Builder)) {
	b.buf.WriteString("<details>\n<summary>")
	b.buf.WriteString(summary)
	b.buf.WriteString("</summary>\n\n")
	body(b)
	b.buf.WriteString("</details>\n\n")
}

// Raw writes s verbatim - the escape hatch for shapes the primitives don't
// cover. Callers own the trailing blank line.
func (b *Builder) Raw(s string) { b.buf.WriteString(s) }

// Rawf writes a formatted string verbatim.
func (b *Builder) Rawf(format string, args ...any) { fmt.Fprintf(&b.buf, format, args...) }

// Bytes returns the accumulated document.
func (b *Builder) Bytes() []byte { return b.buf.Bytes() }

// WriteTo writes the accumulated document to w.
func (b *Builder) WriteTo(w io.Writer) (int64, error) { return b.buf.WriteTo(w) }

// Code renders s as inline code.
func Code(s string) string { return "`" + s + "`" }

// Codes renders labels as comma-separated inline code (a table cell of
// anchors), or "" when there are none.
func Codes(labels []string) string {
	parts := make([]string, len(labels))
	for i, l := range labels {
		parts[i] = Code(l)
	}
	return strings.Join(parts, ", ")
}

// Bold renders s bold.
func Bold(s string) string { return "**" + s + "**" }

// Link renders a markdown link.
func Link(text, href string) string { return "[" + text + "](" + href + ")" }
