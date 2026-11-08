package cache

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/libs/termkit/layout"
	"github.com/egladman/magus/libs/termkit/progress"
)

// The band is plain rows with no frame and no padding, so a drag selection
// copies what the eye reads.
const (
	// progressCols is the width of the progress bar.
	progressCols = 20

	// A status is a word in a four-column slot, then two spaces.
	statusSlot = 4
	statusGap  = "  "
	wordFail   = "FAIL"
	wordOK     = "ok"

	metaSep = layout.Separator
)

// DefaultLoader is the band's spinner when the config names none.
const DefaultLoader = "glyph/dots"

var defaultLoader = mustLoader(DefaultLoader)

func mustLoader(name string) progress.Loader {
	l, err := progress.Lookup(name)
	if err != nil {
		panic(err)
	}
	return l
}

// SetLoader picks the animation the band draws while steps are in flight.
// Only a loader with a fixed footprint fits the slot before a label.
func (h *PrettyHandler) SetLoader(l progress.Loader) error {
	switch l.Kind {
	case progress.Glyph, progress.Rotation, progress.Strip:
	default:
		return fmt.Errorf("the band's loader must be a glyph, rotation or strip loader: %s is a %s loader", l.Name, l.Kind)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loader = l
	// Zero means an injected clock, which cannot move.
	if h.tickEvery > 0 {
		h.tickEvery = l.Interval
	}
	return nil
}

// inFlightLocked reports whether anything is running or waiting to. Callers
// hold h.mu.
func (h *PrettyHandler) inFlightLocked() bool {
	return h.board.inFlight() || h.status.running > 0 || h.status.queued > 0
}

// activeLoader is the configured loader, or the ASCII one where the terminal
// cannot draw Unicode.
func (h *PrettyHandler) activeLoader() progress.Loader {
	if !termkit.WantsUnicode(h.probe) {
		return mustLoader(progress.Fallback)
	}
	if h.loader.Name == "" {
		return defaultLoader
	}
	return h.loader
}

// spinner is the frame for the handler's clock, padded to the loader's slot so
// the label after it does not move. A held clock draws the same frame.
func (h *PrettyHandler) spinner(color bool) []termkit.Span {
	l := h.activeLoader()
	var elapsed time.Duration
	if !h.status.start.IsZero() {
		elapsed = h.now().Sub(h.status.start)
	}
	lines := l.Frame(elapsed)
	if len(lines) == 0 {
		return nil
	}
	spans := slices.Clone(lines[0].Spans)
	if !color {
		for i := range spans {
			spans[i].Style = ""
		}
	}
	if pad := l.Cols - spansCols(spans); pad > 0 {
		spans = append(spans, termkit.Span{Text: strings.Repeat(" ", pad)})
	}
	return spans
}

// spinnerText is the spinner without its styles, for a row that colors its
// own lead.
func (h *PrettyHandler) spinnerText() string {
	var b strings.Builder
	for _, sp := range h.spinner(false) {
		b.WriteString(sp.Text)
	}
	return b.String()
}

// statusWord is a status in its slot, so the text after it lines up.
func statusWord(word string, style termkit.SGR) []termkit.Span {
	return []termkit.Span{
		{Text: word, Style: style},
		{Text: strings.Repeat(" ", statusSlot-len(word)) + statusGap},
	}
}

// statusRow composes the band's first row. A clause with nothing to say is
// omitted, so a steady "0 queued" never takes room.
func (h *PrettyHandler) statusRow(color bool) termkit.Line {
	var bold, dim, red, green termkit.SGR
	if color {
		bold, dim, red, green = termkit.SGRBold, termkit.SGRDim, termkit.SGRRed, termkit.SGRGreen
	}
	s := h.status
	var spans []termkit.Span
	switch {
	case h.inFlightLocked():
		spans = append(h.spinner(color), termkit.Span{Text: " "})
	case s.failed > 0:
		spans = statusWord(wordFail, red)
	default:
		spans = statusWord(wordOK, green)
	}
	lead := len(spans)

	first := true
	clause := func(text string, style termkit.SGR) {
		if !first {
			spans = append(spans, termkit.Span{Text: metaSep, Style: dim})
		}
		first = false
		spans = append(spans, termkit.Span{Text: text, Style: style})
	}
	if done, total := h.board.progress(); total > 0 {
		clause(fmt.Sprintf("%d/%d ", done, total), bold)
		spans = append(spans, progress.Bar(done, total, progressCols, dim).Spans...)
	}
	if s.running > 0 && s.capacity > 0 {
		clause(fmt.Sprintf("%d/%d slots", s.running, s.capacity), bold)
	}
	if s.queued > 0 {
		clause(fmt.Sprintf("%d queued", s.queued), bold)
	}
	if s.passed+s.cached+s.failed > 0 {
		ok := fmt.Sprintf("%d ok", s.passed+s.cached)
		if s.cached > 0 {
			ok += fmt.Sprintf(" (%d cached)", s.cached)
		}
		clause(ok, bold)
	}
	if s.failed > 0 {
		clause(fmt.Sprintf("%d failed", s.failed), bold)
	}
	if !s.start.IsZero() {
		clause(FormatDuration(h.now().Sub(s.start)), dim)
	}
	if first {
		// Nothing follows the lead, so it must not end in its padding.
		spans = trimSpans(spans[:lead])
	}
	return termkit.Line{Spans: spans}
}

// trimSpans drops trailing blanks, so a row never ends in a space.
func trimSpans(spans []termkit.Span) []termkit.Span {
	spans = slices.Clone(spans)
	for len(spans) > 0 {
		last := &spans[len(spans)-1]
		last.Text = strings.TrimRight(last.Text, " ")
		if last.Text != "" {
			break
		}
		spans = spans[:len(spans)-1]
	}
	return spans
}

// bandRow is one row before its metadata is aligned.
type bandRow struct {
	label    []termkit.Span
	meta     string
	selected bool
	// failure indexes the drawn failure list, or is -1.
	failure int
}

func spansCols(spans []termkit.Span) int {
	n := 0
	for _, sp := range spans {
		n += layout.Cols(sp.Text)
	}
	return n
}

// alignBlock lines up the metadata of rows; see [layout.Columns].
func alignBlock(rows []bandRow, width int, selStyle, dim termkit.SGR) []termkit.Line {
	block := make([]layout.Row, len(rows))
	for i, r := range rows {
		block[i] = layout.Row{Label: r.label, Meta: r.meta, Selected: r.selected}
	}
	return layout.Columns(block, width, dim, selStyle)
}

// branch is the tree glyph before a child row.
func branch(last bool) string {
	if last {
		return layout.Tree(1, 2, false)
	}
	return layout.Tree(0, 2, false)
}

// band composes this handler's rows: the status row, the wait board, then the
// failures.
func (h *PrettyHandler) band() []termkit.Line { return h.bandWithin(h.ceilingLocked()) }

// bandWithin composes the band in at most limit rows. The failures keep their
// own ceiling. The wait board takes what is left.
func (h *PrettyHandler) bandWithin(limit int) []termkit.Line {
	// The selection survives NO_COLOR: reverse video is not a color, and
	// without it the prompt does not say what a keypress acts on.
	color := h.WantsColor()
	var dim termkit.SGR
	selStyle := termkit.SGRReverse
	if color {
		dim = termkit.SGRDim
		selStyle += ";" + termkit.SGRBoldRed
	}
	width := h.lease.Width()

	rows := append(make([]termkit.Line, 0, limit), h.statusRow(color))
	h.rowFailure = append(h.rowFailure[:0], -1)

	// Budgeted before drawing so the "+N more" count matches what fits.
	all := h.drawnLocked()
	drawn := all[:fitsInBand(all, h.failureCeilingLocked())]
	hidden := h.status.failed - len(drawn)

	if len(h.preview) > 0 {
		return h.stackPreview(rows, drawn, hidden, limit, width, color, dim, selStyle)
	}
	failLines, failIdx := h.failureBlock(h.failureRows(drawn, color, dim), hidden, width, dim, selStyle)
	room := min(limit-len(rows)-len(failLines), waitBandRows)
	waits := waitLines(h.board.tree(h.now()), room, h.spinnerText(), width, color, dim)
	for range waits {
		h.rowFailure = append(h.rowFailure, -1)
	}
	rows = append(rows, waits...)
	rows = append(rows, failLines...)
	h.rowFailure = append(h.rowFailure, failIdx...)
	return rows
}

// failureRows is the failure tree before alignment.
func (h *PrettyHandler) failureRows(drawn []Failure, color bool, dim termkit.SGR) []bandRow {
	var red, weight termkit.SGR = "", dim
	if color {
		red, weight = termkit.SGRRed, termkit.SGRBoldRed
	}
	var rows []bandRow
	prev := ""
	for i, f := range drawn {
		if f.Project != prev {
			prev = f.Project
			// Once per project: without color the word marks the failure.
			label := append([]termkit.Span{{Text: "  "}}, statusWord(wordFail, red)...)
			label = append(label, termkit.Span{Text: f.Project, Style: dim})
			rows = append(rows, bandRow{failure: -1, label: label})
		}
		last := i == len(drawn)-1 || drawn[i+1].Project != f.Project
		marker := " "
		selected := i == h.selected
		if selected {
			marker = termkit.SelectMark
		}
		row := bandRow{failure: i, selected: selected, label: []termkit.Span{
			{Text: fmt.Sprintf("  %s %s", marker, branch(last)), Style: dim},
			{Text: f.Target, Style: weight},
		}}
		if f.Dur > 0 {
			row.meta = FormatDuration(f.Dur)
		}
		rows = append(rows, row)
	}
	return rows
}

// failureBlock aligns the failure tree and counts the failures not on screen.
// The second result is the failure index each line shows.
func (h *PrettyHandler) failureBlock(rows []bandRow, hidden, width int, dim, selStyle termkit.SGR) ([]termkit.Line, []int) {
	lines := alignBlock(rows, width, selStyle, dim)
	idx := make([]int, 0, len(rows)+1)
	for _, r := range rows {
		idx = append(idx, r.failure)
	}
	// The ring holds five. Without this a run that failed fifty never says so.
	if hidden > 0 {
		lines = append(lines, termkit.Line{
			Text:  fmt.Sprintf("  +%d more above, in the transcript", hidden),
			Style: dim,
		})
		idx = append(idx, -1)
	}
	return lines, idx
}

// stackPreview puts the selected failure's output under the tree. Output in
// focus collapses the tree to the selected failure.
func (h *PrettyHandler) stackPreview(rows []termkit.Line, drawn []Failure, hidden, limit, width int, color bool, dim, selStyle termkit.SGR) []termkit.Line {
	sel := h.selected
	if sel < 0 || sel >= len(drawn) {
		sel = 0
	}
	var lines []termkit.Line
	var idx []int
	if h.focus == FocusPreview && len(drawn) > 0 {
		f := drawn[sel]
		label := []termkit.Span{{Text: fmt.Sprintf("  %s %s %s", termkit.SelectMark, f.Project, f.Target)}}
		meta := fmt.Sprintf("(%d of %d)", sel+1, len(drawn))
		if f.Dur > 0 {
			meta = fmt.Sprintf("%s %s", FormatDuration(f.Dur), meta)
		}
		lines = alignBlock([]bandRow{{label: label, meta: meta, selected: true, failure: sel}}, width, selStyle, dim)
		idx = []int{sel}
	} else {
		lines, idx = h.failureBlock(h.failureRows(drawn, color, dim), hidden, width, dim, selStyle)
	}
	rows = append(rows, lines...)
	h.rowFailure = append(h.rowFailure, idx...)

	// Windowed from the END: the tail is why the output was captured.
	room := limit - len(rows) - 1
	if room < 1 {
		return rows
	}
	out := h.preview
	if len(out) > room {
		out = out[len(out)-room:]
	}
	heading := fmt.Sprintf("output, last %d lines", len(out))
	if len(drawn) > 0 {
		heading = fmt.Sprintf("output of %s %s, last %d lines", drawn[sel].Project, drawn[sel].Target, len(out))
	}
	rows = append(rows, termkit.Line{Text: "  " + heading, Style: dim})
	h.rowFailure = append(h.rowFailure, -1)
	for _, l := range out {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			rows = append(rows, termkit.Line{})
		} else {
			rows = append(rows, termkit.Line{Text: "    " + l, Style: dim})
		}
		h.rowFailure = append(h.rowFailure, -1)
	}
	return rows
}

// FailureHint composes the instruction row for a terminal width columns wide.
// A hint that does not fit is dropped whole, from the right. The way out always
// stays. A width of 0 means no limit.
func FailureHint(width int) []termkit.Line {
	return []termkit.Line{layout.Hints([]layout.Hint{
		{Text: hintSelect},
		{Text: hintRerun, Key: HintKeyRerun},
		{Text: hintFocus, Key: HintKeyFocus},
		{Text: hintCopy, Key: HintKeyCopy},
		{Text: hintOutput, Key: HintKeyOutput},
		{Text: hintDone, Key: HintKeyDone},
	}, width, colDim)}
}
