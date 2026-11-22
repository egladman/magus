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

// LoaderName is a catalog label for display and configuration.
type LoaderName string

// RotationName selects a rotation loader.
type RotationName string

const (
	RotationComet    RotationName = "rotation/comet·1"
	RotationSquare   RotationName = "rotation/square·1"
	RotationClock    RotationName = "rotation/clock·1"
	RotationRing     RotationName = "rotation/ring·2"
	RotationPinwheel RotationName = "rotation/pinwheel·2"
	RotationInfinity RotationName = "rotation/infinity·2"
)

// GlyphName selects a glyph loader.
type GlyphName string

const (
	GlyphDots     GlyphName = "glyph/dots"
	GlyphOrbit    GlyphName = "glyph/orbit"
	GlyphPulse    GlyphName = "glyph/pulse"
	GlyphTriangle GlyphName = "glyph/triangle"
	GlyphSpark    GlyphName = "glyph/spark"
	GlyphLine     GlyphName = "glyph/line"
)

// StripName selects a strip loader.
type StripName string

const (
	StripScanner   StripName = "strip/scanner"
	StripEqualizer StripName = "strip/equalizer"
	StripShade     StripName = "strip/shade"
	StripSnake     StripName = "strip/snake"
	StripSine      StripName = "strip/sine"
	StripDots      StripName = "strip/dots"
)

// TextName selects a text loader.
type TextName string

const (
	TextShimmer    TextName = "text/shimmer"
	TextTypewriter TextName = "text/typewriter"
)

// SpanName selects a span loader.
type SpanName string

const (
	SpanComet    SpanName = "span/comet"
	SpanProgress SpanName = "span/progress"
)

// ColumnName selects a column loader.
type ColumnName string

const (
	ColumnFill     ColumnName = "column/fill"
	ColumnElevator ColumnName = "column/elevator"
)

// Loader is one named animation. Its frames are a pure function of elapsed
// time, so the same instant always draws the same frame.
//
// Cols and Rows are the footprint. A Span loader's Cols and a Column loader's
// Rows are zero until set with [Loader.Width] and [Loader.Height]. A Text
// loader's Cols is its label's width once [Loader.Label] sets one.
type Loader struct {
	Name     LoaderName
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

// Catalog returns every loader in display order.
func Catalog() []Loader { return slices.Clone(catalog) }

// NewRotation builds the selected rotation loader.
func NewRotation(name RotationName) (Loader, error) { return newLoader(Rotation, LoaderName(name)) }

// NewGlyph builds the selected glyph loader.
func NewGlyph(name GlyphName) (Loader, error) { return newLoader(Glyph, LoaderName(name)) }

// NewStrip builds the selected strip loader.
func NewStrip(name StripName) (Loader, error) { return newLoader(Strip, LoaderName(name)) }

// NewText builds the selected text loader.
func NewText(name TextName) (Loader, error) { return newLoader(Text, LoaderName(name)) }

// NewSpan builds the selected span loader.
func NewSpan(name SpanName) (Loader, error) { return newLoader(Span, LoaderName(name)) }

// NewColumn builds the selected column loader.
func NewColumn(name ColumnName) (Loader, error) { return newLoader(Column, LoaderName(name)) }

func newLoader(kind Kind, name LoaderName) (Loader, error) {
	for _, l := range catalog {
		if l.Kind == kind && l.Name == name {
			return l, nil
		}
	}
	var available []string
	for _, loader := range catalog {
		if loader.Kind == kind {
			available = append(available, string(loader.Name))
		}
	}
	return Loader{}, fmt.Errorf("progress: no %s loader %q, choose from %s", kind, name, strings.Join(available, ", "))
}
