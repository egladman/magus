package progress

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/libs/termkit"
)

// Kind is the shape of a loader's footprint.
type Kind string

// The footprints. Glyph, Rotation and Strip have a fixed size. Text animates
// a label, Span fills a width and Column fills a height.
const (
	Glyph    Kind = "glyph"
	Rotation Kind = "rotation"
	Strip    Kind = "strip"
	Text     Kind = "text"
	Span     Kind = "span"
	Column   Kind = "column"
)

// Fallback is the loader to draw when the terminal cannot show Unicode.
const Fallback = "glyph/line"

// Loader is one named animation. Its frames are a pure function of elapsed
// time, so the same instant always draws the same frame.
//
// Cols and Rows are the footprint. A Span loader's Cols and a Column loader's
// Rows are zero until set with [Loader.Width] and [Loader.Height]. A Text
// loader's Cols is its label's width once [Loader.Label] sets one.
type Loader struct {
	Name     string
	Kind     Kind
	Interval time.Duration
	Cols     int
	Rows     int
	label    string
	frame    func(l Loader, f int) [][]cell
}

// cell is one glyph and the style it is drawn in.
type cell struct {
	text  string
	style termkit.SGR
}

// Width returns l sized to cols columns. Only a Span loader takes a width; on
// any other kind it returns l unchanged.
func (l Loader) Width(cols int) Loader {
	if l.Kind == Span {
		l.Cols = max(cols, 0)
	}
	return l
}

// Height returns l sized to rows rows. Only a Column loader takes a height; on
// any other kind it returns l unchanged.
func (l Loader) Height(rows int) Loader {
	if l.Kind == Column {
		l.Rows = max(rows, 0)
	}
	return l
}

// Label returns l animating text. Only a Text loader draws a label; on any
// other kind it returns l unchanged.
func (l Loader) Label(text string) Loader {
	if l.Kind == Text {
		l.label = text
		l.Cols = len([]rune(text))
	}
	return l
}

// Frame draws the frame elapsed after the animation started, one Line per
// row. A negative elapsed draws the first frame. Only a Span loader's rows may
// end in a space. A caller that puts text after a loader pads it to Cols.
func (l Loader) Frame(elapsed time.Duration) []termkit.Line {
	if l.frame == nil || l.Interval <= 0 {
		return nil
	}
	f := int(max(elapsed, 0) / l.Interval)
	rows := l.frame(l, f)
	lines := make([]termkit.Line, 0, len(rows))
	for _, row := range rows {
		if l.Kind != Span {
			row = trimTrailingSpace(row)
		}
		lines = append(lines, termkit.Line{Spans: spans(row)})
	}
	return lines
}

// spans merges neighbouring cells of one style into one span.
func spans(row []cell) []termkit.Span {
	var merged []termkit.Span
	for _, c := range row {
		if c.text == "" {
			continue
		}
		if n := len(merged); n > 0 && merged[n-1].Style == c.style {
			merged[n-1].Text += c.text
			continue
		}
		merged = append(merged, termkit.Span{Text: c.text, Style: c.style})
	}
	return merged
}

func trimTrailingSpace(row []cell) []cell {
	row = slices.Clone(row)
	for len(row) > 0 {
		last := &row[len(row)-1]
		last.text = strings.TrimRight(last.text, " ")
		if last.text != "" {
			break
		}
		row = row[:len(row)-1]
	}
	return row
}

// Names lists every loader in catalog order.
func Names() []string {
	names := make([]string, len(catalog))
	for i, l := range catalog {
		names[i] = l.Name
	}
	return names
}

// Lookup returns the loader called name, such as "glyph/dots". An unknown name
// is an error that lists every name there is.
func Lookup(name string) (Loader, error) {
	for _, l := range catalog {
		if l.Name == name {
			return l, nil
		}
	}
	return Loader{}, fmt.Errorf("progress: no loader %q; the loaders are %s", name, strings.Join(Names(), ", "))
}
