package md

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestBlocksComposeWithSingleBlankLines(t *testing.T) {
	var b Builder
	b.Heading(1, "Title")
	b.Comment("generated")
	b.Paragraph("Some text.")
	b.List("one", "two")
	b.CodeBlock("sh", "magus run build")

	want := "# Title\n\n" +
		"<!-- generated -->\n\n" +
		"Some text.\n\n" +
		"- one\n- two\n\n" +
		"```sh\nmagus run build\n```\n\n"
	if got := string(b.Bytes()); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestHeadingClampsLevel(t *testing.T) {
	var b Builder
	b.Heading(0, "low")
	b.Heading(9, "high")
	got := string(b.Bytes())
	if !strings.HasPrefix(got, "# low\n\n") {
		t.Fatalf("level 0 not clamped to 1: %q", got)
	}
	if !strings.Contains(got, "###### high\n\n") {
		t.Fatalf("level 9 not clamped to 6: %q", got)
	}
}

func TestTableAlignmentAndCells(t *testing.T) {
	var b Builder
	b.Table(
		[]string{"Kind", "Count", "List them"},
		[]Align{Left, Right},
		[][]string{{"spell", "12", "`magus query kind:spell`"}},
	)
	want := "| Kind | Count | List them |\n" +
		"|---|--:|---|\n" +
		"| spell | 12 | `magus query kind:spell` |\n\n"
	if got := string(b.Bytes()); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestTableEmptyRowsIsNoop(t *testing.T) {
	var b Builder
	b.Table([]string{"A"}, nil, nil)
	if len(b.Bytes()) != 0 {
		t.Fatalf("empty table wrote %q", b.Bytes())
	}
}

func TestAlignedCodeBlock(t *testing.T) {
	var b Builder
	b.AlignedCodeBlock("sh", []CodeLine{
		{Code: "magus run build", Note: "compile"},
		{Code: "magus run ci", Note: "full gate"},
		{Code: "plain line"},
	})
	want := "```sh\n" +
		"magus run build  # compile\n" +
		"magus run ci     # full gate\n" +
		"plain line\n" +
		"```\n\n"
	if got := string(b.Bytes()); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestFencedClosesOnError(t *testing.T) {
	var b Builder
	sentinel := errors.New("emit failed")
	err := b.Fenced("mermaid", func(w io.Writer) error {
		io.WriteString(w, "graph LR\n")
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if got := string(b.Bytes()); got != "```mermaid\ngraph LR\n```\n\n" {
		t.Fatalf("fence not closed: %q", got)
	}
}

func TestDetails(t *testing.T) {
	var b Builder
	b.Details("<b>Shared defaults</b>", func(b *Builder) {
		b.CodeBlock("text", "sources  *.go")
	})
	want := "<details>\n<summary><b>Shared defaults</b></summary>\n\n" +
		"```text\nsources  *.go\n```\n\n" +
		"</details>\n\n"
	if got := string(b.Bytes()); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestInlineHelpers(t *testing.T) {
	if got := Code("x"); got != "`x`" {
		t.Fatalf("Code = %q", got)
	}
	if got := Codes([]string{"a", "b"}); got != "`a`, `b`" {
		t.Fatalf("Codes = %q", got)
	}
	if got := Codes(nil); got != "" {
		t.Fatalf("Codes(nil) = %q", got)
	}
	if got := Bold("x"); got != "**x**" {
		t.Fatalf("Bold = %q", got)
	}
	if got := Link("t", "u"); got != "[t](u)" {
		t.Fatalf("Link = %q", got)
	}
}
