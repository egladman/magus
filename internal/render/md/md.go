// Package md is a small typed Markdown builder for magus's generated docs
// (MAGUS.md, the insight report). It replaces hand-concatenated markdown with
// block-level primitives (headings, paragraphs, tables, code fences, lists)
// so table pipes, fence closing, and block spacing are written once here
// instead of at every call site. Every block method leaves exactly one blank
// line after itself, so blocks compose without callers tracking spacing.
//
// Tables come out in dprint's normal form, every column padded to its widest
// cell. That is not cosmetic: magus commits its generated Markdown, and a
// generated file the formatter would rewrite has to be excluded from it, after
// which nothing formats it at all.
//
// It is a builder, not a renderer: output goes wherever the caller writes it
// (emit, never render). Cell and label text is taken verbatim: inputs are
// sanitized at graph ingest, and generated docs deliberately embed inline
// markdown (backticks, bold) in cells.
package md

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Align is a table column alignment, rendered as the GFM delimiter cell.
type Align int

const (
	Left   Align = iota // ---
	Right               // --:
	Center              // :-:
)

// delimiterOf returns the GFM delimiter-row cell for the alignment, filling a
// column of the given width.
func (a Align) delimiterOf(width int) string {
	switch a {
	case Right:
		return strings.Repeat("-", width-1) + ":"
	case Center:
		return ":" + strings.Repeat("-", width-2) + ":"
	}
	return strings.Repeat("-", width)
}

// pad fills a cell out to the column width, putting the spare space where the
// alignment wants the text: a centered cell that cannot split its padding
// evenly leans left, as dprint does.
func (a Align) pad(cell string, width int) string {
	spare := width - utf8.RuneCountInString(cell)
	switch a {
	case Right:
		return strings.Repeat(" ", spare) + cell
	case Center:
		return strings.Repeat(" ", spare/2) + cell + strings.Repeat(" ", spare-spare/2)
	}
	return cell + strings.Repeat(" ", spare)
}

// minWidth is the narrowest column the alignment can be written in: a colon at
// each end still needs a dash between them.
func (a Align) minWidth() int {
	switch a {
	case Right:
		return 2
	case Center:
		return 3
	}
	return 1
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

// Quote writes a blockquote block, one "> line" per entry. Unlike Comment it renders,
// so it suits a note the reader is meant to see. No-op when empty.
func (b *Builder) Quote(lines ...string) {
	if len(lines) == 0 {
		return
	}
	for _, line := range lines {
		b.buf.WriteString("> ")
		b.buf.WriteString(line)
		b.buf.WriteString("\n")
	}
	b.buf.WriteString("\n")
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
	for _, line := range TableLines(header, align, rows) {
		b.buf.WriteString(line)
		b.buf.WriteByte('\n')
	}
	b.buf.WriteByte('\n')
}

// Table renders a GFM table as a Markdown block: the padded lines plus the
// blank line that ends the block. For a generator that assembles its page as a
// string rather than through a Builder.
func Table(header []string, align []Align, rows [][]string) string {
	return strings.Join(TableLines(header, align, rows), "\n") + "\n\n"
}

// TableLines renders a GFM table as its lines, every column padded to its
// widest cell and no column narrower than its delimiter needs. That is the form
// dprint writes, so a generated file can be committed and still pass the
// formatter instead of earning an entry in dprint.json's excludes. Callers that
// build a page with fmt.Fprintf join the lines themselves; Builder.Table is the
// same table with the block spacing.
//
// Width is counted in runes, which matches dprint for everything magus emits
// (ASCII plus the occasional narrow symbol) and understates a wide CJK cell.
func TableLines(header []string, align []Align, rows [][]string) []string {
	widths := make([]int, len(header))
	for i, cell := range header {
		widths[i] = max(utf8.RuneCountInString(cell), alignAt(align, i).minWidth())
	}
	for _, r := range rows {
		for i, cell := range r {
			widths[i] = max(widths[i], utf8.RuneCountInString(cell))
		}
	}
	line := func(cells []string) string {
		padded := make([]string, len(header))
		for i := range header {
			padded[i] = alignAt(align, i).pad(cells[i], widths[i])
		}
		return "| " + strings.Join(padded, " | ") + " |"
	}
	delimiters := make([]string, len(header))
	for i, w := range widths {
		delimiters[i] = alignAt(align, i).delimiterOf(w)
	}
	lines := make([]string, 0, 2+len(rows))
	lines = append(lines, line(header), line(delimiters))
	for _, r := range rows {
		lines = append(lines, line(r))
	}
	return lines
}

// alignAt is the alignment of column i: Left past the end of align, so a caller
// may pass nil or a short slice.
func alignAt(align []Align, i int) Align {
	if i < len(align) {
		return align[i]
	}
	return Left
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

// Raw writes s verbatim, the escape hatch for shapes the primitives don't
// cover. Callers own the trailing blank line.
func (b *Builder) Raw(s string) { b.buf.WriteString(s) }

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
