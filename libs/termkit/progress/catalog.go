package progress

import (
	"math"
	"strings"
	"time"

	"github.com/egladman/magus/libs/termkit"
)

// The palette the frames are drawn in.
const (
	acc  = termkit.SGRGreen
	acc2 = termkit.SGRBlue
	warn = termkit.SGRYellow
	mid  = termkit.SGR("")
	lo   = termkit.SGRDim
	hi   = termkit.SGRBold
)

const (
	stripCols = 8
	eighths   = " ▏▎▍▌▋▊▉█"
	rise      = " ▁▂▃▄▅▆▇█"
)

var catalog = []Loader{
	braille1("rotation/comet·1", 70, acc, cometCell),
	cycle("rotation/square·1", Rotation, 130, warn, "◰◳◲◱"),
	cycle("rotation/clock·1", Rotation, 130, acc2, "◴◷◶◵"),
	braille2("rotation/ring·2", 60, acc, ringFrame),
	braille2("rotation/pinwheel·2", 80, warn, pinwheelFrame),
	braille2("rotation/infinity·2", 50, acc2, infinityFrame),
	cycle("glyph/dots", Glyph, 80, acc, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"),
	cycle("glyph/orbit", Glyph, 80, acc, "⣾⣽⣻⢿⡿⣟⣯⣷"),
	cycle("glyph/pulse", Glyph, 120, acc2, "⠀⣀⣤⣶⣿⣶⣤⣀"),
	cycle("glyph/triangle", Glyph, 120, acc2, "◢◣◤◥"),
	cycle("glyph/spark", Glyph, 110, warn, "·✢✳✶✻✽✻✶✳✢"),
	cycle("glyph/line", Glyph, 120, mid, `|/-\`),
	strip("strip/scanner", 70, scannerFrame),
	strip("strip/equalizer", 90, equalizerFrame),
	strip("strip/shade", 90, shadeFrame),
	strip("strip/snake", 70, snakeFrame),
	strip("strip/sine", 60, sineFrame),
	strip("strip/dots", 300, dotsFrame),
	{Name: "text/shimmer", Kind: Text, Interval: 60 * time.Millisecond, Rows: 1, frame: shimmerFrame},
	{Name: "text/typewriter", Kind: Text, Interval: 70 * time.Millisecond, Rows: 1, frame: typewriterFrame},
	{Name: "span/comet", Kind: Span, Interval: 30 * time.Millisecond, Rows: 1, frame: spanCometFrame},
	{Name: "span/progress", Kind: Span, Interval: 40 * time.Millisecond, Rows: 1, frame: spanProgressFrame},
	{Name: "column/fill", Kind: Column, Interval: 60 * time.Millisecond, Cols: 1, frame: fillFrame},
	{Name: "column/elevator", Kind: Column, Interval: 80 * time.Millisecond, Cols: 1, frame: elevatorFrame},
}

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// jsRound is JavaScript's Math.round, which rounds halves up. The frames are
// ported from JavaScript, and math.Round would move a pixel at every half.
func jsRound(x float64) int { return int(math.Floor(x + 0.5)) }

func row(cells ...cell) [][]cell { return [][]cell{cells} }

func cycle(name string, kind Kind, every int, style termkit.SGR, frames string) Loader {
	runes := []rune(frames)
	return Loader{Name: name, Kind: kind, Interval: ms(every), Cols: 1, Rows: 1,
		frame: func(_ Loader, f int) [][]cell {
			return row(cell{string(runes[f%len(runes)]), style})
		}}
}

func strip(name string, every int, frame func(f int) []cell) Loader {
	return Loader{Name: name, Kind: Strip, Interval: ms(every), Cols: stripCols, Rows: 1,
		frame: func(_ Loader, f int) [][]cell { return [][]cell{frame(f)} }}
}

// pixel is one braille dot, x across and y down.
type pixel struct{ x, y int }

type pixels map[pixel]bool

func set(pts ...pixel) pixels {
	s := pixels{}
	for _, p := range pts {
		s[p] = true
	}
	return s
}

// braille draws on a canvas of cols cells by one, each cell two dots wide and
// four tall.
func braille(cols int, on pixels) string {
	bits := [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}
	var b strings.Builder
	for cx := range cols {
		v := rune(0)
		for py := range 4 {
			for px := range 2 {
				if on[pixel{cx*2 + px, py}] {
					v |= bits[py][px]
				}
			}
		}
		b.WriteRune(0x2800 + v)
	}
	return b.String()
}

func braille1(name string, every int, style termkit.SGR, frame func(f int) pixels) Loader {
	return Loader{Name: name, Kind: Rotation, Interval: ms(every), Cols: 1, Rows: 1,
		frame: func(_ Loader, f int) [][]cell { return row(cell{braille(1, frame(f)), style}) }}
}

func braille2(name string, every int, style termkit.SGR, frame func(f int) pixels) Loader {
	return Loader{Name: name, Kind: Rotation, Interval: ms(every), Cols: 2, Rows: 1,
		frame: func(_ Loader, f int) [][]cell { return row(cell{braille(2, frame(f)), style}) }}
}

// cellRing is the rim of one braille cell, clockwise from the top left.
var cellRing = []pixel{{0, 0}, {1, 0}, {1, 1}, {1, 2}, {1, 3}, {0, 3}, {0, 2}, {0, 1}}

func cometCell(f int) pixels {
	n := len(cellRing)
	var pts []pixel
	for k := range 3 {
		pts = append(pts, cellRing[(f-k+n*9)%n])
	}
	return set(pts...)
}

// ring is the rim of a w-by-h canvas, clockwise from the top left.
func ring(w, h int) []pixel {
	var p []pixel
	for x := range w {
		p = append(p, pixel{x, 0})
	}
	for y := 1; y < h; y++ {
		p = append(p, pixel{w - 1, y})
	}
	for x := w - 2; x >= 0; x-- {
		p = append(p, pixel{x, h - 1})
	}
	for y := h - 2; y >= 1; y-- {
		p = append(p, pixel{0, y})
	}
	return p
}

// ray is the pixels of a segment of length r from (cx, cy) at angle a.
func ray(cx, cy, r, a float64) []pixel {
	var pts []pixel
	for t := 0.0; t <= r; t += 0.25 {
		pts = append(pts, pixel{jsRound(cx + math.Cos(a)*t), jsRound(cy + math.Sin(a)*t)})
	}
	return pts
}

func ringFrame(f int) pixels {
	r := ring(4, 4)
	n := len(r)
	var pts []pixel
	for k := range 4 {
		pts = append(pts, r[(f-k+n*9)%n])
	}
	return set(pts...)
}

func pinwheelFrame(f int) pixels {
	a := float64(f%8) / 8 * math.Pi / 2
	var pts []pixel
	for k := range 4 {
		pts = append(pts, ray(1.5, 1.5, 2.2, a+float64(k)*math.Pi/2)...)
	}
	return set(pts...)
}

func infinityFrame(f int) pixels {
	var pts []pixel
	for k := range 4 {
		t := float64((f-k)%24) / 24 * math.Pi * 2
		pts = append(pts, pixel{jsRound(1.5 + 1.6*math.Sin(t)), jsRound(1.5 + 1.5*math.Sin(t)*math.Cos(t)*1.8)})
	}
	return set(pts...)
}

func scannerFrame(f int) []cell {
	n := 2 * (stripCols - 1)
	p := f % n
	x, dir := n-p, 1
	if p < stripCols {
		x, dir = p, -1
	}
	cells := make([]cell, stripCols)
	for i := range stripCols {
		switch (i - x) * dir {
		case 0:
			cells[i] = cell{"●", acc}
		case 1:
			cells[i] = cell{"◉", acc}
		case 2:
			cells[i] = cell{"○", mid}
		default:
			cells[i] = cell{"·", lo}
		}
	}
	return cells
}

func equalizerFrame(f int) []cell {
	bars := []rune(rise)
	cells := make([]cell, stripCols)
	for i := range stripCols {
		v := jsRound((math.Sin(float64(f)*0.45+float64(i)*0.9)*0.5+0.5)*7) + 1
		cells[i] = cell{string(bars[v]), acc}
	}
	return cells
}

func shadeFrame(f int) []cell {
	ramp := []string{"░", "▒", "▓", "█", "▓", "▒", "░"}
	period := stripCols + 4
	cells := make([]cell, stripCols)
	for i := range stripCols {
		k := ((i-f)%period + period) % period
		if k < len(ramp) {
			cells[i] = cell{ramp[k], acc2}
		} else {
			cells[i] = cell{"░", lo}
		}
	}
	return cells
}

// snakePath is a loop round a 16-by-4 dot canvas, clockwise.
var snakePath = func() []pixel {
	var p []pixel
	for x := range 16 {
		p = append(p, pixel{x, 0})
	}
	for y := 1; y < 4; y++ {
		p = append(p, pixel{15, y})
	}
	for x := 14; x >= 0; x-- {
		p = append(p, pixel{x, 3})
	}
	for y := 2; y >= 1; y-- {
		p = append(p, pixel{0, y})
	}
	return p
}()

func snakeFrame(f int) []cell {
	n := len(snakePath)
	var pts []pixel
	for k := range 6 {
		pts = append(pts, snakePath[(f-k+n*10)%n])
	}
	return []cell{{braille(stripCols, set(pts...)), acc}}
}

func sineFrame(f int) []cell {
	on := pixels{}
	for x := range stripCols * 2 {
		y := jsRound(1.5 + 1.5*math.Sin((float64(x)+float64(f)*0.6)*0.55))
		on[pixel{x, y}] = true
	}
	return []cell{{braille(stripCols, on), acc2}}
}

func dotsFrame(f int) []cell {
	k := f % 4
	return []cell{
		{strings.Repeat("·", k), mid},
		{strings.Repeat("·", 3-k), lo},
		{strings.Repeat(" ", stripCols-3), lo},
	}
}

func shimmerFrame(l Loader, f int) [][]cell {
	label := []rune(l.label)
	p := f%(len(label)+12) - 6
	cells := make([]cell, len(label))
	for i, r := range label {
		d := math.Abs(float64(i - p))
		style := lo
		switch {
		case d < 1.5:
			style = hi
		case d < 4:
			style = mid
		}
		cells[i] = cell{string(r), style}
	}
	return [][]cell{cells}
}

func typewriterFrame(l Loader, f int) [][]cell {
	label := []rune(l.label)
	n := f % (len(label) + 20)
	cells := []cell{{string(label[:min(n, len(label))]), hi}}
	if (f/6)%2 == 1 {
		cells = append(cells, cell{"▌", acc})
	}
	return [][]cell{cells}
}

func spanCometFrame(l Loader, f int) [][]cell {
	width := l.Cols
	head := f % (width + 20)
	cells := make([]cell, width)
	for i := range width {
		d := head - i
		switch {
		case d == 0:
			cells[i] = cell{"━", hi}
		case d > 0 && d < 6:
			cells[i] = cell{"━", acc}
		case d >= 6 && d < 14:
			cells[i] = cell{"━", mid}
		default:
			cells[i] = cell{"─", lo}
		}
	}
	return [][]cell{cells}
}

func spanProgressFrame(l Loader, f int) [][]cell {
	width := l.Cols
	frac := float64(f%300) / 300
	filled := frac * float64(width)
	full := int(math.Floor(filled))
	part := int(math.Floor((filled - float64(full)) * 8))
	bar := strings.Repeat("█", full)
	if full < width && part > 0 {
		bar += string([]rune(eighths)[part])
	}
	used := full
	if part > 0 {
		used++
	}
	return row(cell{bar, acc}, cell{strings.Repeat("░", max(0, width-used)), lo})
}

func fillFrame(l Loader, f int) [][]cell {
	height := l.Rows
	bars := []rune(rise)
	v := float64(f%160) / 160 * float64(height) * 8
	rows := make([][]cell, 0, height)
	for r := height - 1; r >= 0; r-- {
		lvl := max(0, min(8, int(math.Floor(v-float64(r)*8))))
		if lvl > 0 {
			rows = append(rows, []cell{{string(bars[lvl]), acc}})
		} else {
			rows = append(rows, []cell{{"░", lo}})
		}
	}
	return rows
}

func elevatorFrame(l Loader, f int) [][]cell {
	height := l.Rows
	if height == 0 {
		return nil
	}
	n := 2 * (height - 1)
	y := 0
	if n > 0 {
		p := f % n
		y = n - p
		if p < height {
			y = p
		}
	}
	rows := make([][]cell, 0, height)
	for r := range height {
		switch {
		case r == y:
			rows = append(rows, []cell{{"█", warn}})
		case r-y == 1 || y-r == 1:
			rows = append(rows, []cell{{"▒", mid}})
		default:
			rows = append(rows, []cell{{"│", lo}})
		}
	}
	return rows
}
