package cache

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/interactive/tty"
)

// The band is plain rows with no frame and no padding, so a drag selection
// copies what the eye reads.
const (
	// spinnerEvery is how long one spinner frame shows.
	spinnerEvery = 80 * time.Millisecond
	// progressCols is the width of the progress bar.
	progressCols = 20
	// maxLabelCols caps the column the metadata aligns to.
	maxLabelCols = 60
	// minLabelCols is the narrowest a label is clipped to for metadata.
	minLabelCols = 8
	// metaGap separates the longest label from its metadata.
	metaGap = 2

	glyphFail = "✗"
	glyphOK   = "✓"

	treeTee = "├ "
	treeEnd = "╰ "

	metaSep = " · "
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// progressEighths are the partial cells of the bar, one eighth to seven.
var progressEighths = []string{"▏", "▎", "▍", "▌", "▋", "▊", "▉"}

const (
	barFull  = "█"
	barTrack = "░"
)

// inFlightLocked reports whether anything is running or waiting to. Callers
// hold h.mu.
func (h *PrettyHandler) inFlightLocked() bool {
	return h.board.inFlight() || h.status.running > 0 || h.status.queued > 0
}

// spinner is the frame for the handler's clock, so a held clock draws the same
// frame every time.
func (h *PrettyHandler) spinner() string {
	if h.status.start.IsZero() {
		return spinnerFrames[0]
	}
	frame := int(h.now().Sub(h.status.start) / spinnerEvery)
	return spinnerFrames[max(frame, 0)%len(spinnerFrames)]
}

// progressBar draws done of total in progressCols cells, in eighths.
func progressBar(done, total int) (filled, track string) {
	eighths := min(max(done, 0), total) * progressCols * 8 / total
	full := eighths / 8
	filled = strings.Repeat(barFull, full)
	used := full
	if part := eighths % 8; part > 0 {
		filled += progressEighths[part-1]
		used++
	}
	return filled, strings.Repeat(barTrack, progressCols-used)
}

// statusRow composes the band's first row. A clause with nothing to say is
// omitted, so a steady "0 queued" never takes room.
func (h *PrettyHandler) statusRow(color bool) tty.Line {
	var bold, dim, red, green tty.SGR
	if color {
		bold, dim, red, green = tty.SGRBold, tty.SGRDim, tty.SGRRed, tty.SGRGreen
	}
	s := h.status
	var spans []tty.Span
	switch {
	case h.inFlightLocked():
		spans = append(spans, tty.Span{Text: h.spinner(), Style: bold})
	case s.failed > 0:
		spans = append(spans, tty.Span{Text: glyphFail, Style: red})
	default:
		spans = append(spans, tty.Span{Text: glyphOK, Style: green})
	}
	spans = append(spans, tty.Span{Text: " "})

	first := true
	clause := func(text string, style tty.SGR) {
		if !first {
			spans = append(spans, tty.Span{Text: metaSep, Style: dim})
		}
		first = false
		spans = append(spans, tty.Span{Text: text, Style: style})
	}
	if done, total := h.board.progress(); total > 0 {
		clause(fmt.Sprintf("%d/%d ", done, total), bold)
		filled, track := progressBar(done, total)
		spans = append(spans, tty.Span{Text: filled})
		if track != "" {
			spans = append(spans, tty.Span{Text: track, Style: dim})
		}
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
		spans = spans[:1]
	}
	return tty.Line{Spans: spans}
}

// bandRow is one row before its metadata is aligned.
type bandRow struct {
	label    []tty.Span
	meta     string
	selected bool
	// failure indexes the drawn failure list, or is -1.
	failure int
}

func spansCols(spans []tty.Span) int {
	n := 0
	for _, sp := range spans {
		n += tty.Cols(sp.Text)
	}
	return n
}

// clipSpans shortens spans to w columns from the end, marking each cut.
func clipSpans(spans []tty.Span, w int) []tty.Span {
	out := slices.Clone(spans)
	excess := spansCols(out) - w
	for i := len(out) - 1; i >= 0 && excess > 0; i-- {
		c := tty.Cols(out[i].Text)
		out[i].Text = tty.Fit(out[i].Text, max(c-excess, 0))
		excess -= c - tty.Cols(out[i].Text)
	}
	return out
}

// alignBlock starts the metadata of every row in one column after the longest
// label. The label gives way on a narrow terminal, never the metadata.
// The gap is mid-row, so no row ends in a space.
func alignBlock(rows []bandRow, width int, selStyle, dim tty.SGR) []tty.Line {
	labelW, metaW := 0, 0
	for _, r := range rows {
		if r.meta == "" {
			continue
		}
		labelW = max(labelW, spansCols(r.label))
		metaW = max(metaW, tty.Cols(r.meta))
	}
	labelW = min(labelW, maxLabelCols)
	if width > 0 {
		labelW = min(labelW, max(width-metaGap-metaW, minLabelCols))
	}
	lines := make([]tty.Line, 0, len(rows))
	for _, r := range rows {
		spans := slices.Clone(r.label)
		if r.meta != "" {
			spans = clipSpans(spans, labelW)
			pad := labelW - spansCols(spans) + metaGap
			spans = append(spans,
				tty.Span{Text: strings.Repeat(" ", pad)},
				tty.Span{Text: r.meta, Style: dim})
		}
		if r.selected {
			for i := range spans {
				spans[i].Style = selStyle
			}
		}
		lines = append(lines, tty.Line{Spans: spans})
	}
	return lines
}

// band composes this handler's rows: the status row, the wait board, then the
// failures.
func (h *PrettyHandler) band() []tty.Line { return h.bandWithin(h.ceilingLocked()) }

// bandWithin composes the band in at most limit rows. The failures keep their
// own ceiling. The wait board takes what is left.
func (h *PrettyHandler) bandWithin(limit int) []tty.Line {
	// The selection survives NO_COLOR: reverse video is not a color, and
	// without it the prompt does not say what a keypress acts on.
	color := h.WantsColor()
	var dim tty.SGR
	selStyle := tty.SGRReverse
	if color {
		dim = tty.SGRDim
		selStyle += ";" + tty.SGRBoldRed
	}
	width := h.lease.Width()

	rows := append(make([]tty.Line, 0, limit), h.statusRow(color))
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
	waits := waitLines(h.board.tree(h.now()), room, h.spinner(), width, color, dim)
	for range waits {
		h.rowFailure = append(h.rowFailure, -1)
	}
	rows = append(rows, waits...)
	rows = append(rows, failLines...)
	h.rowFailure = append(h.rowFailure, failIdx...)
	return rows
}

// failureRows is the failure tree before alignment.
func (h *PrettyHandler) failureRows(drawn []Failure, color bool, dim tty.SGR) []bandRow {
	var red, weight tty.SGR = "", dim
	if color {
		red, weight = tty.SGRRed, tty.SGRBoldRed
	}
	var rows []bandRow
	prev := ""
	for i, f := range drawn {
		if f.Project != prev {
			prev = f.Project
			// Once per project: the only mark of a failure without color.
			rows = append(rows, bandRow{failure: -1, label: []tty.Span{
				{Text: "  "},
				{Text: glyphFail, Style: red},
				{Text: " " + f.Project, Style: dim},
			}})
		}
		branch := treeTee
		if i == len(drawn)-1 || drawn[i+1].Project != f.Project {
			branch = treeEnd
		}
		marker := " "
		selected := i == h.selected
		if selected {
			marker = tty.SelectMark
		}
		row := bandRow{failure: i, selected: selected, label: []tty.Span{
			{Text: fmt.Sprintf("  %s %s", marker, branch), Style: dim},
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
func (h *PrettyHandler) failureBlock(rows []bandRow, hidden, width int, dim, selStyle tty.SGR) ([]tty.Line, []int) {
	lines := alignBlock(rows, width, selStyle, dim)
	idx := make([]int, 0, len(rows)+1)
	for _, r := range rows {
		idx = append(idx, r.failure)
	}
	// The ring holds five. Without this a run that failed fifty never says so.
	if hidden > 0 {
		lines = append(lines, tty.Line{
			Text:  fmt.Sprintf("  +%d more above, in the transcript", hidden),
			Style: dim,
		})
		idx = append(idx, -1)
	}
	return lines, idx
}

// stackPreview puts the selected failure's output under the tree. Output in
// focus collapses the tree to the selected failure.
func (h *PrettyHandler) stackPreview(rows []tty.Line, drawn []Failure, hidden, limit, width int, color bool, dim, selStyle tty.SGR) []tty.Line {
	sel := h.selected
	if sel < 0 || sel >= len(drawn) {
		sel = 0
	}
	var lines []tty.Line
	var idx []int
	if h.focus == FocusPreview && len(drawn) > 0 {
		f := drawn[sel]
		label := []tty.Span{{Text: fmt.Sprintf("  %s %s %s", tty.SelectMark, f.Project, f.Target)}}
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
	rows = append(rows, tty.Line{Text: "  " + heading, Style: dim})
	h.rowFailure = append(h.rowFailure, -1)
	for _, l := range out {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			rows = append(rows, tty.Line{})
		} else {
			rows = append(rows, tty.Line{Text: "    " + l, Style: dim})
		}
		h.rowFailure = append(h.rowFailure, -1)
	}
	return rows
}

type hintItem struct{ text, key string }

// FailureHint composes the instruction row for a terminal width columns wide.
// A hint that does not fit is dropped whole, from the right. The way out always
// stays. A width of 0 means no limit.
func FailureHint(width int) []tty.Line {
	hints := []hintItem{
		{hintSelect, ""},
		{hintRerun, HintKeyRerun},
		{hintFocus, HintKeyFocus},
		{hintCopy, HintKeyCopy},
		{hintOutput, HintKeyOutput},
	}
	done := hintItem{hintDone, HintKeyDone}
	keep := len(hints)
	for keep > 0 && width > 0 && hintsCols(hints[:keep], done) > width {
		keep--
	}
	var spans []tty.Span
	for _, hi := range append(hints[:keep:keep], done) {
		if len(spans) > 0 {
			spans = append(spans, tty.Span{Text: metaSep, Style: colDim})
		}
		spans = append(spans, tty.Span{Text: hi.text, Style: colDim, Key: hi.key})
	}
	return []tty.Line{{Spans: spans}}
}

func hintsCols(hints []hintItem, done hintItem) int {
	n := tty.Cols(done.text)
	for _, hi := range hints {
		n += tty.Cols(hi.text) + tty.Cols(metaSep)
	}
	return n
}
