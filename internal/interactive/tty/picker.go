// The interactive list picker. The package doc lives in probe.go.
//
// Items are filtered by an AND substring search over whitespace-split
// tokens of the filter input. Render goes to stderr so stdout stays
// clean for downstream pipes; the caller is expected to have already
// verified that stdin and stderr are TTYs.
//
// It is driven by keys OR the mouse. Hovering moves the highlight, a click
// selects, a double click chooses - so nothing has to be remembered to use it.
// The mouse needs the terminal to report where the cursor is; one that will not
// leaves the picker keyboard-only, which is what it always was.

package tty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// pickerRules is what the box costs in rows: one rule above, one below. The
// prompt rides the top and the way out rides the bottom, so the list keeps
// every line it had.
const pickerRules = 2

// SelectMark is the glyph every interactive surface uses to show which row a
// keypress or a click will act on.
//
// One constant rather than one per surface: the picker and the run's failure
// band are two lists a reader learns to drive the same way, and they were
// marking the current row with different characters - so the same gesture
// looked like a different affordance depending on which one was open. A ">" is
// a keyboard character standing in for a pointer; a triangle IS one, and it
// belongs to the same geometric family as the pool gauge's squares.
//
// The glyph only. Each surface pads it to whatever its own row shape needs.
const SelectMark = "\u25b8"

// pickerHint is the way out, drawn on the prompt line. Short enough to sit
// beside a filter on a narrow terminal, and stating the two things a reader
// cannot discover by looking: how to leave, and how to clear what they typed.
const pickerHint = "[esc] cancel  [ctrl-u] clear"

// ErrAborted is returned when the user presses ESC, Ctrl-C, or Ctrl-D.
var ErrAborted = errors.New("picker: aborted")

// PickOptions configures a single Pick call.
type PickOptions struct {
	// Prompt is the label drawn before the filter input (e.g. "project").
	Prompt string
	// InitialFilter pre-populates the filter string. The picker filters
	// items against it on first paint.
	InitialFilter string
	// Initial is the index in items that should be highlighted on first
	// paint when the post-filter list is non-empty and contains it.
	Initial int
	// MaxRows caps the visible window of matches. Defaults to 10.
	MaxRows int
	// Query replaces the built-in substring filter with a live lookup, called
	// once per change to the filter text and expected to return the items to
	// show, best first.
	//
	// It exists so a picker can search something larger than the list it was
	// opened with - the knowledge graph, which knows about files, symbols and
	// docs as well as the projects the caller could enumerate up front. The
	// caller keeps the mapping from the returned label back to whatever it
	// means; this type only draws strings.
	//
	// Nil keeps the substring filter, which is the right behaviour for a list
	// that is already complete.
	Query func(filter string) []string
}

// Pick blocks until the user selects an item or aborts. On success it returns
// the index into the original items slice. On abort it returns -1 and
// ErrAborted.
//
// Takes its descriptors and probe like every other entry point in this package
// rather than reaching for os.Stdin and os.Stderr itself. It was the one
// interactive surface a test could not redirect, which is exactly backwards for
// the one that reads keys.
func Pick(in *os.File, out io.Writer, p Probe, items []string, opts PickOptions) (int, error) {
	if len(items) == 0 && opts.Query == nil {
		return -1, errors.New("picker: no items")
	}
	if opts.MaxRows <= 0 {
		opts.MaxRows = 10
	}

	// One input session for keys AND mouse. The picker used to decode keys
	// itself, which meant two decoders in one package disagreeing about which
	// control bytes exist - Ctrl-N and Ctrl-P were documented here and dropped
	// on the floor, because they are below 0x20 and fell through to "printable".
	input, err := OpenInput(in, out, p)
	if err != nil {
		return -1, fmt.Errorf("picker: %w", err)
	}
	defer func() { _ = input.Close() }()

	s := &session{
		items:   items,
		opts:    opts,
		filter:  opts.InitialFilter,
		out:     out,
		probe:   p,
		view:    NewInlineView(out, p),
		mouseOK: true,
	}
	s.queryRow = func() (int, bool) {
		row, _, ok := input.CursorPosition()
		return row, ok
	}
	s.refilter()
	s.cursor = s.findInitial()
	s.draw()
	defer s.cleanup()

	for {
		ev, err := input.Read()
		if err != nil {
			return -1, ErrAborted
		}

		if ev.Kind == EventMouse {
			i, hit := s.matchAt(ev.Row)
			if !hit {
				// Off the list. Deliberately not a choice and not an abort: a
				// stray click must never pick something, least of all something
				// the pointer was not even over.
				continue
			}
			if ev.Motion {
				// Hovering moves the highlight, so what a click would take is
				// never a guess.
				s.hover(i)
				continue
			}
			// Left button only, which also means the WHEEL is left alone. The
			// wheel is how a reader scrolls their own transcript, and magus
			// does not take it on any surface - a picker that swallowed it
			// would break scrollback for the seconds it is open, which is the
			// behaviour that makes other tools unusable inside tmux.
			if !ev.Press || ev.Button != MouseLeft {
				continue
			}
			s.cursor = i
			if ev.Clicks == 2 {
				return s.matches[s.cursor], nil
			}
			s.draw()
			continue
		}

		switch ev.Key {
		case KeyEnter:
			if len(s.matches) == 0 {
				continue
			}
			return s.matches[s.cursor], nil
		case KeyEscape, KeyCtrlC, KeyCtrlD:
			return -1, ErrAborted
		case KeyUp, KeyCtrlP:
			if len(s.matches) > 0 {
				s.cursor = (s.cursor - 1 + len(s.matches)) % len(s.matches)
			}
		case KeyDown, KeyCtrlN:
			if len(s.matches) > 0 {
				s.cursor = (s.cursor + 1) % len(s.matches)
			}
		case KeyBackspace:
			if len(s.filter) > 0 {
				// Strip one rune.
				rs := []rune(s.filter)
				s.filter = string(rs[:len(rs)-1])
				s.refilter()
				s.cursor = 0
			}
		case KeyCtrlU:
			s.filter = ""
			s.refilter()
			s.cursor = 0
		case KeyRune:
			s.filter += string(ev.Rune)
			s.refilter()
			s.cursor = 0
		default:
			continue
		}
		s.draw()
	}
}

// RenderPick returns the block a picker WOULD draw for the given state, without
// opening a terminal or running an input loop.
//
// For the documentation renderer, which publishes pictures of this surface: a
// hand-written imitation drifts from the real thing silently, and the drift
// gate cannot see it because it compares the renderer against itself. Driving
// the real composition means a change to the picker's frame shows up in the
// docs as a stale-SVG failure.
func RenderPick(out io.Writer, p Probe, items []string, opts PickOptions, filter string, cursor int) string {
	if opts.MaxRows <= 0 {
		opts.MaxRows = 10
	}
	s := &session{items: items, opts: opts, filter: filter, out: out, probe: p}
	s.refilter()
	if cursor >= 0 && cursor < len(s.matches) {
		s.cursor = cursor
	}
	return s.frame()
}

type session struct {
	items   []string
	opts    PickOptions
	filter  string
	matches []int // indices into items, post-filter
	cursor  int   // index into matches
	out     io.Writer
	probe   Probe
	view    *InlineView

	// start and visible describe the window the last draw showed, so a click
	// coordinate can be mapped back to the match it landed on.
	start, visible int
	// promptRow is the absolute terminal row of the prompt line, learned by
	// asking the terminal. mouseOK goes false the first time it will not say,
	// and stays false: re-asking a terminal that does not answer would cost the
	// timeout on every keystroke and make the picker feel broken.
	promptRow int
	mouseOK   bool
	// queryRow asks the terminal for the cursor row. A field so a test can
	// count the round trips, which is the thing worth counting: each one is a
	// full RTT to the terminal, and over ssh that is tens of milliseconds.
	queryRow func() (int, bool)
	// lastHeight is the terminal height the position was last anchored
	// against. A change means the window was resized and the arithmetic below
	// no longer describes reality.
	lastHeight int
}

// filterIndices returns the indices of items matching filter under the
// AND-substring rule.
func filterIndices(items []string, filter string) []int {
	tokens := strings.Fields(strings.ToLower(filter))
	out := make([]int, 0, len(items))
	for i, it := range items {
		lc := strings.ToLower(it)
		ok := true
		for _, t := range tokens {
			if !strings.Contains(lc, t) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, i)
		}
	}
	return out
}

func (s *session) refilter() {
	if s.opts.Query == nil {
		s.matches = filterIndices(s.items, s.filter)
		return
	}
	// A live query REPLACES the item list rather than narrowing it, so the
	// indices the picker returns have to index the new list. Keeping items and
	// matches in step here is what lets the rest of the session stay identical
	// for both modes.
	s.items = s.opts.Query(s.filter)
	s.matches = make([]int, len(s.items))
	for i := range s.items {
		s.matches[i] = i
	}
}

func (s *session) findInitial() int {
	for j, idx := range s.matches {
		if idx == s.opts.Initial {
			return j
		}
	}
	return 0
}

func (s *session) draw() {
	before := s.view.Lines()
	s.view.Paint(s.frame())
	s.reposition(before, s.view.Lines())
}

// frame composes the block: the visible window of matches, then the prompt.
//
// It returns a string rather than writing it, so [InlineView] can compare it
// against what is already on screen and rewrite only the lines that differ.
// Typing changes every row and costs what it always did; moving the highlight
// changes two rows, and now costs two.
func (s *session) frame() string {
	var b strings.Builder
	maxRows := s.maxRows()
	visible := maxRows
	if visible > len(s.matches) {
		visible = len(s.matches)
	}
	// Window the visible slice around the cursor.
	start := 0
	if s.cursor >= maxRows {
		start = s.cursor - maxRows + 1
	}
	end := start + visible
	if end > len(s.matches) {
		end = len(s.matches)
		start = end - visible
		if start < 0 {
			start = 0
		}
	}
	s.start, s.visible = start, visible

	prompt := s.opts.Prompt
	if prompt == "" {
		prompt = "filter"
	}
	// The SAME box the run's pinned band draws, from the same functions.
	//
	// The picker used to be a bare list with its hint jammed onto the prompt
	// row, which made two surfaces a reader drives identically look like two
	// different products. Position still differs on purpose - this is drawn
	// where the cursor is, because `magus x` is a command you type and its list
	// belongs where you typed it - but the LOOK does not.
	//
	// The prompt rides the top rule and the way out rides the bottom, so
	// neither costs a row of the list.
	inner := s.innerWidth()
	dim := plain
	if WantsColor(s.out, s.probe) {
		dim = func(t string) string { return Colorize(t, SGRDim) }
	}
	b.WriteString(boxRule(inner, boxTL, boxTR, prompt+": "+s.filter+"_", "", dim))
	b.WriteString("\n")
	for i := start; i < end; i++ {
		idx := s.matches[i]
		marker := "  "
		if i == s.cursor {
			marker = SelectMark + " "
		}
		b.WriteString(boxWrap(ClipVisible(marker+s.items[idx], inner), inner, dim))
		b.WriteString("\n")
	}
	if len(s.matches) == 0 && maxRows > 0 {
		b.WriteString(boxWrap("  (no matches)", inner, dim))
		b.WriteString("\n")
		s.visible = 1
	}
	b.WriteString(boxRule(inner, boxBL, boxBR, pickerHint, "", dim))
	return b.String()
}

// innerWidth is the columns a picker row has for its own text: the terminal
// less the two vertical edges, and less the column held back from the wrap.
func (s *session) innerWidth() int {
	w, ok := s.width()
	if !ok || w < minUsefulWidth {
		w = minUsefulWidth
	}
	return w - 1 - 2*borderCols
}

// reposition keeps track of where the prompt line is after a redraw, asking the
// terminal only when it has to.
//
// It has to exactly once. Every later move is ARITHMETIC: the block is redrawn
// from where the old one started, so the prompt lands `newLines-oldLines` rows
// from where it was, clamped to the last row when growing pushed the screen up.
//
// The round trip it avoids is not a micro-cost. Asking is microseconds on a
// local terminal and a full round trip over ssh, and the block changes height
// on any keystroke that filters the list past the visible rows - so querying
// per redraw put an RTT of lag on most of the typing, on exactly the connection
// where lag is already the problem.
//
// optimization: derive the prompt row instead of asking the terminal for it.
//
//	measured: TestSessionAsksTheTerminalOnlyOnce pins one query per session
//	          where the old code issued one per height change; the query costs
//	          149us on a local pty and a full RTT remotely.
//	trade-off: the row is derived, so any movement this model does not predict
//	          goes unnoticed until a resize re-anchors it. The model covers the
//	          only two causes - the block changing height, and growing at the
//	          bottom scrolling the screen.
func (s *session) reposition(oldLines, newLines int) {
	if !s.mouseOK {
		return
	}
	height, ok := s.height()
	if !ok {
		return
	}
	if s.promptRow == 0 || height != s.lastHeight {
		// First draw, or the window was resized and the arithmetic no longer
		// describes anything. Re-anchor against the terminal.
		s.lastHeight = height
		s.locate()
		return
	}
	row := s.promptRow + (newLines - oldLines)
	if row > height {
		// Growing at the bottom scrolled the screen, so the prompt is on the
		// last row and everything above it moved up.
		row = height
	}
	s.promptRow = row
}

// maxRows is how many items the window may show, bounded by what the terminal
// can actually display.
//
// Without the bound the picker asks for its configured ten rows plus a prompt
// on a terminal that may be shorter, [InlineView.Paint] refuses the block
// because erasing it would walk off the top of the screen, and the picker draws
// NOTHING - while still sitting in a raw-mode read loop. A blank terminal with
// no echo and no prompt is indistinguishable from a hung process, and an
// eleven-row window is an ordinary split pane rather than an exotic case.
//
// Two rows are held back: one for the prompt line, and one so the block stays
// strictly shorter than the screen, which is what makes redrawing it in place
// possible at all.
func (s *session) maxRows() int {
	max := s.opts.MaxRows
	height, ok := s.height()
	if !ok {
		return max
	}
	// The box's two rules come out of the budget as well as the row the caller
	// is standing on. Without that a short terminal drew a list that did not
	// fit, pushing its own prompt off the top - which reads as the picker
	// having hung rather than as a window being small.
	if limit := height - 1 - pickerRules; max > limit {
		max = limit
	}
	if max < 0 {
		max = 0
	}
	return max
}

// height reports the terminal's height. A local ioctl, not a round trip, so it
// is safe to ask on every redraw.
// width reports the terminal's column count, for deciding whether an optional
// piece of the prompt line fits. Sibling of height.
func (s *session) width() (int, bool) {
	if s.probe == nil {
		return 0, false
	}
	fd, ok := Fd(s.out)
	if !ok {
		return 0, false
	}
	w, _, err := s.probe.Size(fd)
	if err != nil || w <= 0 {
		return 0, false
	}
	return w, true
}

func (s *session) height() (int, bool) {
	if s.probe == nil {
		return 0, false
	}
	fd, ok := Fd(s.out)
	if !ok {
		return 0, false
	}
	_, h, err := s.probe.Size(fd)
	if err != nil || h <= 0 {
		return 0, false
	}
	return h, true
}

// hover moves the highlight to i, redrawing only if it actually moved.
//
// The guard is the whole point. Any-event tracking reports EVERY cell the
// pointer crosses, and a row is many cells wide, so a sweep across one item
// would otherwise repaint the list once per column - identical bytes, over and
// over, for a picture that did not change.
//
// optimization: skip the redraw when the highlight did not move.
//
//	measured: BenchmarkPickerMouseSweep 41.12k -> 4.626k B_written/sweep
//	          (-88.8%), 50.50us -> 23.12us sec/op (benchstat, n=8).
//	trade-off: none. The skipped redraws produced byte-identical output; what
//	          is saved is the terminal parsing and rendering them, and on a
//	          remote link, carrying them.
func (s *session) hover(i int) {
	if i == s.cursor {
		return
	}
	s.cursor = i
	s.draw()
}

// locate asks the terminal where the prompt line ended up, which is what makes
// a click resolvable against a view drawn wherever the cursor happened to be.
func (s *session) locate() {
	if s.queryRow == nil || !s.mouseOK {
		return
	}
	row, ok := s.queryRow()
	if !ok {
		s.mouseOK = false
		return
	}
	s.promptRow = row
}

// matchAt maps an absolute terminal row to the match index drawn on it, if any.
//
// Items sit directly above the prompt line, so the topmost is `visible` rows up
// from it. A click on the prompt, or anywhere off the block, matches nothing -
// and must not be treated as a selection.
func (s *session) matchAt(row int) (int, bool) {
	if !s.mouseOK || s.visible == 0 || len(s.matches) == 0 {
		return 0, false
	}
	offset := row - (s.promptRow - s.visible)
	if offset < 0 || offset >= s.visible {
		return 0, false
	}
	i := s.start + offset
	if i < 0 || i >= len(s.matches) {
		return 0, false
	}
	return i, true
}

func (s *session) cleanup() {
	// Erase our drawing so the parent terminal looks like the picker never ran.
	// The caller prints whatever permanent output it wants.
	_ = s.view.Clear()
}
