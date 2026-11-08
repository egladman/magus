package layout

import (
	"slices"
	"strings"

	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/libs/termkit/internal/cells"
)

const (
	// MaxLabel caps the column [Columns] aligns metadata to.
	MaxLabel = 60
	// minLabel is the narrowest a label is clipped to so its metadata fits.
	minLabel = 8
	// gap separates the longest label from its metadata.
	gap = 2
	// Separator joins metadata items and hints.
	Separator = " · "
)

// Cols is the display width of s in terminal columns. Escape sequences count
// for nothing and a multi-byte rune counts as one.
func Cols(s string) int { return cells.Cols(s) }

// Fit shortens text to width columns, ending in "..." when it cut. A width of
// zero or less returns "".
func Fit(text string, width int) string { return cells.Fit(text, width) }

// Clip shortens s to cols columns without an ellipsis, never cutting inside an
// escape sequence and closing any style the cut left open.
func Clip(s string, cols int) string { return cells.Clip(s, cols) }

// ClipBytes shortens plain text to nBytes bytes, ellipsis included, never
// splitting a rune. Styled text needs [Clip].
func ClipBytes(s string, nBytes int) string { return cells.ClipBytes(s, nBytes) }

// Row is one record before [Columns] aligns it: a label read first, then dim
// metadata. A row with no Meta is drawn as its label alone.
type Row struct {
	Label    []termkit.Span
	Meta     string
	Selected bool
}

// Columns starts the metadata of every row one column after the longest label,
// capped at [MaxLabel]. On a narrow terminal the label gives way, never the
// metadata. The gap sits between two words, so no row ends in a space. A width
// of 0 means no limit. Every span of a selected row takes the selected style.
func Columns(rows []Row, width int, meta, selected termkit.SGR) []termkit.Line {
	labelW, metaW := 0, 0
	for _, r := range rows {
		if r.Meta == "" {
			continue
		}
		labelW = max(labelW, spansCols(r.Label))
		metaW = max(metaW, Cols(r.Meta))
	}
	labelW = min(labelW, MaxLabel)
	if width > 0 {
		labelW = min(labelW, max(width-gap-metaW, minLabel))
	}
	lines := make([]termkit.Line, 0, len(rows))
	for _, r := range rows {
		spans := slices.Clone(r.Label)
		if r.Meta != "" {
			spans = clipSpans(spans, labelW)
			pad := labelW - spansCols(spans) + gap
			spans = append(spans,
				termkit.Span{Text: strings.Repeat(" ", pad)},
				termkit.Span{Text: r.Meta, Style: meta})
		}
		if r.Selected {
			for i := range spans {
				spans[i].Style = selected
			}
		}
		lines = append(lines, termkit.Line{Spans: spans})
	}
	return lines
}

func spansCols(spans []termkit.Span) int {
	n := 0
	for _, sp := range spans {
		n += Cols(sp.Text)
	}
	return n
}

// clipSpans shortens spans to w columns from the end, marking each cut.
func clipSpans(spans []termkit.Span, w int) []termkit.Span {
	out := slices.Clone(spans)
	excess := spansCols(out) - w
	for i := len(out) - 1; i >= 0 && excess > 0; i-- {
		c := Cols(out[i].Text)
		out[i].Text = Fit(out[i].Text, max(c-excess, 0))
		excess -= c - Cols(out[i].Text)
	}
	return out
}

// Tree returns the branch drawn before item i of n siblings: a tee for every
// item but the last, which closes the branch. With ascii set it draws the
// fallback for a terminal that is not UTF-8.
func Tree(i, n int, ascii bool) string {
	last := i >= n-1
	switch {
	case ascii && last:
		return `\- `
	case ascii:
		return "|- "
	case last:
		return "╰ "
	default:
		return "├ "
	}
}

// Hint is one instruction in a hint row. A non-empty Key makes the span a
// click target that [termkit.Zone.HitSpan] reports.
type Hint struct {
	Text string
	Key  string
}

// Hints joins hints into one row no wider than width columns. Hints that do not
// fit are dropped whole from the right, and the last one always stays, since it
// is the way out. A width of 0 means no limit.
func Hints(hints []Hint, width int, style termkit.SGR) termkit.Line {
	if len(hints) == 0 {
		return termkit.Line{}
	}
	rest, last := hints[:len(hints)-1], hints[len(hints)-1]
	keep := len(rest)
	for keep > 0 && width > 0 && hintsCols(rest[:keep], last) > width {
		keep--
	}
	var spans []termkit.Span
	for _, h := range append(rest[:keep:keep], last) {
		if len(spans) > 0 {
			spans = append(spans, termkit.Span{Text: Separator, Style: style})
		}
		spans = append(spans, termkit.Span{Text: h.Text, Style: style, Key: h.Key})
	}
	return termkit.Line{Spans: spans}
}

func hintsCols(hints []Hint, last Hint) int {
	n := Cols(last.Text)
	for _, h := range hints {
		n += Cols(h.Text) + Cols(Separator)
	}
	return n
}
