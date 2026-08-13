package screen

import (
	"fmt"
	"strconv"
	"strings"
)

// SVG renders what the terminal currently shows as a standalone SVG picture.
//
// SVG rather than a raster format for three reasons that all matter to a
// repository. It is TEXT, so a rendered terminal diffs like source and a change
// to the output shows up in review as the lines that changed rather than as an
// opaque blob. It needs no player, so a docs page embeds it with an <img> or
// inline and nothing has to load. And it scales, so the same asset is legible
// in a sidebar thumbnail and at full width.
//
// Deterministic by construction: no timestamps, no randomness, no map
// iteration. The same grid renders byte-for-byte the same picture, which is
// what lets a generated asset be committed and gated for drift.
func (s *Screen) SVG(opts SVGOptions) string {
	opts = opts.withDefaults()
	var b strings.Builder
	width := opts.CellWidth*s.width + 2*opts.Pad
	height := opts.LineHeight*s.height + 2*opts.Pad

	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" font-family="%s" font-size="%d">`,
		width, height, width, height, opts.FontFamily, opts.FontSize)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" rx="%d" fill="%s"/>`,
		width, height, opts.Radius, opts.Theme.Background)
	b.WriteString(s.svgBody(opts))
	b.WriteString(`</svg>`)
	return b.String()
}

// svgBody renders the grid's text without the surrounding document, so a frame
// can be composed into an animation alongside its siblings.
func (s *Screen) svgBody(opts SVGOptions) string {
	var b strings.Builder
	for row := range s.height {
		baseline := opts.Pad + opts.LineHeight*row + opts.Baseline
		for _, run := range s.runs(row) {
			if strings.TrimSpace(run.text) == "" && !run.style.reverse {
				continue
			}
			x := opts.Pad + opts.CellWidth*run.col
			fill, bg, weight, opacity := opts.Theme.resolve(run.style)
			if bg != "" {
				fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`,
					x, opts.Pad+opts.LineHeight*row, opts.CellWidth*len([]rune(run.text)), opts.LineHeight, bg)
			}
			fmt.Fprintf(&b, `<text x="%d" y="%d" fill="%s"`, x, baseline, fill)
			if weight != "" {
				fmt.Fprintf(&b, ` font-weight="%s"`, weight)
			}
			if opacity != "" {
				fmt.Fprintf(&b, ` opacity="%s"`, opacity)
			}
			// textLength pins the run to the same cell grid the rect above is sized
			// from. Without it the run is laid out at whatever advance the viewer's
			// font happens to have, and a rune the font draws wider than CellWidth
			// pushes the tail of the run past its own highlight - where a reversed
			// run, drawn in the background colour, becomes invisible. The box-drawing
			// and arrow runes this package uses are East Asian AMBIGUOUS width, so
			// that is the common case rather than the exotic one.
			//
			// A terminal gets this for free: the cell grid IS the advance. An SVG has
			// to say so.
			fmt.Fprintf(&b, ` textLength="%d" xml:space="preserve">%s</text>`,
				opts.CellWidth*len([]rune(run.text)), escapeXML(run.text))
		}
	}
	return b.String()
}

// run is a stretch of cells on one row sharing a style, which is how the text
// reaches SVG: one <text> element per run rather than per cell, because a
// hundred-column row is one element instead of a hundred.
type run struct {
	col   int
	text  string
	style sgrState
}

// runs coalesces a row into styled stretches, ignoring the blank tail.
//
// A terminal row is padded to its full width with spaces, and drawing them
// would put a hundred columns of nothing into every committed asset. A styled
// blank is kept: reverse video paints a filled cell, and a run of those is the
// selected row of an interactive band.
func (s *Screen) runs(row int) []run {
	cells := s.cells[row]
	end := len(cells)
	for end > 0 {
		c := cells[end-1]
		if c.r != ' ' || c.style != "" {
			break
		}
		end--
	}
	var out []run
	var cur *run
	for col, c := range cells[:end] {
		st := parseSGR(c.style)
		if cur != nil && cur.style == st {
			cur.text += string(c.r)
			continue
		}
		out = append(out, run{col: col, text: string(c.r), style: st})
		cur = &out[len(out)-1]
	}
	return out
}

// sgrState is the subset of SGR this renders: the attributes magus emits.
type sgrState struct {
	bold    bool
	dim     bool
	reverse bool
	fg      int // 30-37, or 0 for the default foreground
}

// parseSGR reads the semicolon-separated parameters of one SGR sequence.
// Unknown parameters are ignored rather than guessed at, matching the rule the
// rest of this package follows.
func parseSGR(s string) sgrState {
	var st sgrState
	if s == "" {
		return st
	}
	for _, part := range strings.Split(s, ";") {
		n, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		switch {
		case n == 1:
			st.bold = true
		case n == 2:
			st.dim = true
		case n == 7:
			st.reverse = true
		case n >= 30 && n <= 37:
			st.fg = n
		}
	}
	return st
}

func escapeXML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
