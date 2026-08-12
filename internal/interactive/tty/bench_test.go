package tty

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// benchItems builds a realistic picker list: `magus x` offers project+target
// pairs, so a large monorepo puts thousands of them in front of the filter.
func benchItems(n int) []string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf("internal/service/component-%04d:build", i)
	}
	return items
}

// BenchmarkFilter measures the work done on EVERY keystroke: the whole item
// list is re-scanned to recompute the match set.
func BenchmarkFilter(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		items := benchItems(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_ = Filter(items, "service comp")
			}
		})
	}
}

// BenchmarkSessionDraw measures one repaint of the picker, which happens on
// every keystroke AND on every mouse-motion event.
func BenchmarkSessionDraw(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		items := benchItems(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			s := &session{items: items, opts: Options{MaxRows: 10}, out: io.Discard}
			s.refilter()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				s.draw()
			}
		})
	}
}

// BenchmarkInputDecodeMouseMotion measures per-event decode cost. Any-event
// tracking emits one of these for every cell the pointer crosses, so this is
// the most frequent single operation in the whole interactive path.
func BenchmarkInputDecodeMouseMotion(b *testing.B) {
	stream := strings.Repeat("\x1b[<35;12;20M", 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i += 1024 {
		in, _ := newTestInput(stream)
		for range 1024 {
			if _, err := in.decode(); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkRegionRender covers both halves of the frame diff: a frame that
// changed, and one that did not. The unchanged case is the steady state of any
// view repainted on a timer.
func BenchmarkRegionRender(b *testing.B) {
	rows := []Row{
		{Spans: []Span{{Text: "pool 6/8 running   9 ok  1 failed", Style: SGRDim}, {Text: "6.4s", Style: SGRDim, Align: AlignRight}}},
		{Text: "[fail] test internal/sandbox (ran, 4.1s)", Style: SGRBoldRed},
		{Text: "[fail] lint std (ran, 2.3s)", Style: SGRBoldRed},
		{}, {}, {},
	}
	b.Run("unchanged", func(b *testing.B) {
		r := newBenchRegion()
		_ = r.Render(rows)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_ = r.Render(rows)
		}
	})
	b.Run("changed", func(b *testing.B) {
		r := newBenchRegion()
		alt := append([]Row(nil), rows...)
		alt[1] = Row{Text: "[fail] test internal/proc (ran, 9.9s)", Style: SGRBoldRed}
		b.ReportAllocs()
		b.ResetTimer()
		for i := range b.N {
			if i%2 == 0 {
				_ = r.Render(alt)
				continue
			}
			_ = r.Render(rows)
		}
	})
}

func newBenchRegion() *Region {
	r := NewRegion(discardTTY{}, 6, terminal(120, 40))
	_ = r.Reserve()
	return r
}

// discardTTY is a terminal-shaped sink: it has a descriptor so the region
// enables, and throws the bytes away so the benchmark measures composition
// rather than the write.
type discardTTY struct{}

func (discardTTY) Write(p []byte) (int, error) { return len(p), nil }
func (discardTTY) Fd() uintptr                 { return 2 }

// countingTTY records how many bytes reach the terminal. For an interactive
// surface that is the number that matters: ns/op measures composition, but what
// a reader actually waits on is the terminal parsing and rendering the bytes -
// and, over ssh, the link carrying them.
type countingTTY struct{ n int }

func (c *countingTTY) Write(p []byte) (int, error) { c.n += len(p); return len(p), nil }
func (*countingTTY) Fd() uintptr                   { return 2 }

// BenchmarkPickerMouseSweep is one pointer sweep down the list.
//
// Any-event tracking reports every CELL the pointer crosses, not every row, and
// a row is as wide as the terminal - so a diagonal sweep across ten items is
// dozens of events of which only ten change anything. The two variants are the
// picker before and after the redraw is guarded on the highlight actually
// moving.
func BenchmarkPickerMouseSweep(b *testing.B) {
	items := benchItems(1000)
	// A pointer crossing ten rows, eight cells wide each: 80 motion events.
	sweep := func(s *session, onEvent func(*session, int)) {
		for row := 10; row < 20; row++ {
			for range 8 {
				if i, ok := s.matchAt(row); ok {
					onEvent(s, i)
				}
			}
		}
	}
	run := func(b *testing.B, onEvent func(*session, int)) {
		var total int
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			c := &countingTTY{}
			p := terminal(120, 40)
			s := &session{items: items, opts: Options{MaxRows: 10}, out: c, probe: p,
				view: NewInlineView(c, p), mouseOK: true, promptRow: 20}
			s.refilter()
			s.draw()
			c.n = 0
			sweep(s, onEvent)
			total = c.n
		}
		b.ReportMetric(float64(total), "B_written/sweep")
	}
	b.Run("redraw-every-event", func(b *testing.B) {
		run(b, func(s *session, i int) { s.cursor = i; s.draw() })
	})
	b.Run("redraw-on-change", func(b *testing.B) {
		run(b, func(s *session, i int) { s.hover(i) })
	})
}

// BenchmarkPickerArrowNavigation is holding down an arrow key: the filter does
// not change, so only the two highlighted rows differ between frames.
func BenchmarkPickerArrowNavigation(b *testing.B) {
	items := benchItems(1000)
	run := func(b *testing.B, whole bool) {
		var total int
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			c := &countingTTY{}
			p := terminal(120, 40)
			s := &session{items: items, opts: Options{MaxRows: 10}, out: c, probe: p,
				view: NewInlineView(c, p)}
			s.refilter()
			s.draw()
			c.n = 0
			for range 10 {
				s.cursor = (s.cursor + 1) % len(s.matches)
				if whole {
					// What it did before: forget the last frame, so every line
					// counts as changed and the whole block is rewritten.
					s.view.Reset()
				}
				s.draw()
			}
			total = c.n
		}
		b.ReportMetric(float64(total), "B_written/10keys")
	}
	b.Run("rewrite-whole-block", func(b *testing.B) { run(b, true) })
	b.Run("diff-changed-lines", func(b *testing.B) { run(b, false) })
}

// statusFrame is a realistic `magus status --watch` grid: mostly static rows
// with one animated cell, which is what the 150ms tick exists to advance.
func statusFrame(spinner rune) string {
	var b strings.Builder
	fmt.Fprintf(&b, "daemon  %c  running   pid 4211   uptime 3h12m\n", spinner)
	b.WriteString("telemetry\n  enabled\ttrue\n  endpoint\tlocalhost:4317\n  protocol\tgrpc\n")
	for i := range 12 {
		fmt.Fprintf(&b, "  project-%02d\tbuild\tcached\t0.0s\n", i)
	}
	return b.String()
}

// BenchmarkWatchFrame is one second of a watch view. Grid mode animates at
// 150ms, so this cost repeats for as long as the view is open - and over ssh,
// on the wire the whole time. The two variants are the block rewritten whole
// and the block diffed.
func BenchmarkWatchFrame(b *testing.B) {
	spinners := []rune{'|', '/', '-', '\\'}
	run := func(b *testing.B, whole bool) {
		var total int
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			c := &countingTTY{}
			v := NewInlineView(c, terminal(100, 40))
			v.Paint(statusFrame(spinners[0]))
			c.n = 0
			for i := 1; i <= 7; i++ {
				if whole {
					// What it did before: forget the last frame, so every line
					// counts as changed.
					v.Reset()
				}
				v.Paint(statusFrame(spinners[i%len(spinners)]))
			}
			total = c.n
		}
		b.ReportMetric(float64(total), "B_written/sec")
	}
	b.Run("rewrite-whole-block", func(b *testing.B) { run(b, true) })
	b.Run("diff-changed-lines", func(b *testing.B) { run(b, false) })
}
