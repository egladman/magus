package screen

import (
	"fmt"
	"strings"
)

// SVGOptions is the geometry and palette one rendering uses. The zero value is
// usable: every field falls back to a default sized for a readable docs asset.
type SVGOptions struct {
	FontFamily string
	FontSize   int
	CellWidth  int // advance width of one character cell
	LineHeight int
	Baseline   int // distance from a row's top to its text baseline
	Pad        int
	Radius     int
	Theme      Theme
}

func (o SVGOptions) withDefaults() SVGOptions {
	if o.FontFamily == "" {
		// A stack, not a webfont: an embedded font would multiply the size of
		// every committed asset, and a linked one would not resolve for a
		// reader offline or a docs build with no network.
		o.FontFamily = "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
	}
	if o.FontSize == 0 {
		o.FontSize = 13
	}
	if o.CellWidth == 0 {
		// 0.6 em is the advance of essentially every monospace face at this
		// size. Getting it wrong does not misalign the text - each run is
		// positioned absolutely - it only leaves the columns loose or tight.
		o.CellWidth = 8
	}
	if o.LineHeight == 0 {
		o.LineHeight = 18
	}
	if o.Baseline == 0 {
		o.Baseline = 13
	}
	if o.Pad == 0 {
		o.Pad = 12
	}
	if o.Radius == 0 {
		o.Radius = 6
	}
	if o.Theme == (Theme{}) {
		o.Theme = DarkTheme
	}
	return o
}

// Theme maps the attributes magus emits to colors. Only the eight SGR
// foregrounds it actually uses are named; anything else renders as the default,
// which is the same restraint the emulator keeps about sequences it does not
// know.
type Theme struct {
	Background string
	Foreground string
	Red        string
	Green      string
	Yellow     string
	Blue       string
	Magenta    string
	Cyan       string
	White      string
	Black      string
}

// DarkTheme is the default: a modern terminal profile rather than the sixteen
// ANSI primaries, which are harsh at small sizes on a page.
var DarkTheme = Theme{
	Background: "#14161b",
	Foreground: "#d5d1ca",
	Black:      "#14161b",
	Red:        "#e2695f",
	Green:      "#79bd76",
	Yellow:     "#d6b158",
	Blue:       "#6ba3c4",
	Magenta:    "#b98bc4",
	Cyan:       "#57a8a8",
	White:      "#d5d1ca",
}

// LightTheme renders on a page that is not dark. Kept deliberately close to
// DarkTheme in hue so a docs page can carry both without the two reading as
// different products.
var LightTheme = Theme{
	Background: "#fbfaf7",
	Foreground: "#22242a",
	Black:      "#22242a",
	Red:        "#b03028",
	Green:      "#3f7a3c",
	Yellow:     "#8a6a15",
	Blue:       "#2f6a8c",
	Magenta:    "#7b4b8a",
	Cyan:       "#2b6f6f",
	White:      "#22242a",
}

// resolve turns one run's attributes into the fill, background, weight and
// opacity that draw it.
//
// Reverse video is drawn as a real swap - a filled rectangle behind
// background-colored text - because it marks the selected row of an
// interactive band and has to read as selected in a picture, not merely as
// differently colored.
func (t Theme) resolve(st sgrState) (fill, bg, weight, opacity string) {
	fill = t.Foreground
	switch st.fg {
	case 30:
		fill = t.Black
	case 31:
		fill = t.Red
	case 32:
		fill = t.Green
	case 33:
		fill = t.Yellow
	case 34:
		fill = t.Blue
	case 35:
		fill = t.Magenta
	case 36:
		fill = t.Cyan
	case 37:
		fill = t.White
	}
	if st.bold {
		weight = "bold"
	}
	if st.dim {
		opacity = "0.55"
	}
	if st.reverse {
		bg, fill = fill, t.Background
		opacity = ""
	}
	return fill, bg, weight, opacity
}

// Animate renders a sequence of frames as one self-contained animated SVG.
//
// No JavaScript and no player: each frame is a group whose opacity is driven by
// SMIL, which every browser that renders SVG at all supports. That matters for
// a docs site with a strict content policy, where a script-driven player would
// simply not run.
//
// Frames must share a size; the first one sets it. A frame is shown for its own
// hold time, and the whole sequence loops - a terminal recording that stops on
// the last frame reads as a page that failed to load.
func Animate(frames []*Screen, holds []float64, opts SVGOptions) (string, error) {
	if len(frames) == 0 {
		return "", fmt.Errorf("screen: no frames to animate")
	}
	if len(holds) != len(frames) {
		return "", fmt.Errorf("screen: %d frames but %d hold times", len(frames), len(holds))
	}
	opts = opts.withDefaults()

	total := 0.0
	starts := make([]float64, len(frames))
	for i, h := range holds {
		if h <= 0 {
			return "", fmt.Errorf("screen: frame %d has a non-positive hold", i)
		}
		starts[i] = total
		total += h
	}

	first := frames[0]
	width := opts.CellWidth*first.width + 2*opts.Pad
	height := opts.LineHeight*first.height + 2*opts.Pad

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" font-family="%s" font-size="%d">`,
		width, height, width, height, opts.FontFamily, opts.FontSize)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" rx="%d" fill="%s"/>`,
		width, height, opts.Radius, opts.Theme.Background)

	for i, f := range frames {
		if f.width != first.width || f.height != first.height {
			return "", fmt.Errorf("screen: frame %d is %dx%d, want %dx%d",
				i, f.width, f.height, first.width, first.height)
		}
		// Every frame starts hidden and is switched on for its own window, so
		// the first paint is not a flash of all of them stacked.
		fmt.Fprintf(&b, `<g opacity="0">%s`, f.svgBody(opts))
		// calcMode="discrete" is load-bearing, not decoration. The default is
		// linear, which INTERPOLATES between key times - so a frame does not
		// switch off at the end of its window, it fades out across everything
		// after it, and all the frames end up superimposed at partial opacity.
		// Discrete switches, which is what a sequence of stills needs.
		//
		// The trailing 0 is what makes the last interval hidden: under discrete
		// each value holds until the NEXT key time, so a three-value list would
		// leave the frame visible from its window to the end of the loop.
		fmt.Fprintf(&b,
			`<animate attributeName="opacity" values="0;1;0;0" keyTimes="0;%s;%s;1" calcMode="discrete" dur="%ss" repeatCount="indefinite"/>`,
			trimFloat(starts[i]/total), trimFloat((starts[i]+holds[i])/total), trimFloat(total))
		b.WriteString(`</g>`)
	}
	b.WriteString(`</svg>`)
	return b.String(), nil
}

// trimFloat formats without a trailing zero run, so the same sequence renders
// byte-identically rather than drifting with formatting.
func trimFloat(f float64) string {
	s := fmt.Sprintf("%.4f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
