package progress

import (
	"io"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/libs/termkit/internal/cells"
)

// SpinLoader is the loader a [Spinner] turns where the terminal can draw
// Unicode: the one magus's own run band turns by default, so a script's wait
// looks like magus's.
const SpinLoader = "glyph/dots"

// Spinner is a loader turning beside a label on one line, redrawn in place
// until [Spinner.Stop].
type Spinner struct {
	stop     sync.Once
	done     chan struct{}
	finished chan struct{}
}

// StartSpinner starts a loader turning beside label on out and returns at once;
// the caller does its blocking work and then calls [Spinner.Stop].
//
// It draws whatever out is: the caller decides whether out is a terminal worth
// drawing on. Anything else written to out while it turns lands under the
// loader's line.
func StartSpinner(out io.Writer, p termkit.Probe, label string) (*Spinner, error) {
	return startSpinner(out, p, label, time.Now)
}

// Stop erases the line and returns once nothing more will be written, so the
// caller's next write cannot interleave with a frame. Safe to call more than
// once, from any goroutine.
func (s *Spinner) Stop() {
	s.stop.Do(func() { close(s.done) })
	<-s.finished
}

func startSpinner(out io.Writer, p termkit.Probe, label string, now func() time.Time) (*Spinner, error) {
	name := SpinLoader
	if !termkit.WantsUnicode(p) {
		name = Fallback
	}
	l, err := Lookup(name)
	if err != nil {
		return nil, err
	}
	color := termkit.WantsColor(out, p)
	view := termkit.NewInlineView(out, p)
	start := now()
	s := &Spinner{done: make(chan struct{}), finished: make(chan struct{})}
	go func() {
		defer close(s.finished)
		tick := time.NewTicker(l.Interval)
		defer tick.Stop()
		for {
			view.Paint(spinFrame(l, now().Sub(start), label, color))
			select {
			case <-s.done:
				_ = view.Clear() // a view: a terminal that refuses the erase keeps a stale line, and the caller's own result matters more
				return
			case <-tick.C:
			}
		}
	}()
	return s, nil
}

// spinFrame is the one row drawn at elapsed: the loader padded to its footprint,
// so the label beside it does not move as the frames change width.
func spinFrame(l Loader, elapsed time.Duration, label string, color bool) string {
	var b strings.Builder
	used := 0
	// A glyph loader draws one row; a taller one shows only its first.
	if lines := l.Frame(elapsed); len(lines) > 0 {
		for _, span := range lines[0].SpansCopy() {
			used += cells.Cols(span.Text)
			if color && span.Style != "" {
				b.WriteString(termkit.Colorize(span.Text, span.Style))
				continue
			}
			b.WriteString(span.Text)
		}
	}
	if used < l.Cols {
		b.WriteString(strings.Repeat(" ", l.Cols-used))
	}
	if label != "" {
		b.WriteString(" ")
		b.WriteString(label)
	}
	return b.String()
}
