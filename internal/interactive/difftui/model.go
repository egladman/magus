// Package difftui is the terminal client of the shared diff session: the same changeset
// the console's Diff surface renders and an agent joins over MCP, read with a keyboard.
//
// The CLI already shared the review COMPUTATION; what it did not share was the
// COORDINATION - where the human is looking, which hunks they have read, what an agent
// wants them to look at. That is the whole difference between three tools rendering one
// changeset and three clients of one review.
//
// The model in this file owns navigation, the generated fold, the viewed set and the
// viewport window, and it touches no terminal at all: rows in, rows out. render.go turns
// it into a frame and run.go feeds it keys, so the state machine is testable without a pty.
package difftui

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/diff"
	"github.com/egladman/magus/types"
)

// Hunk is one @@ section as the viewer shows it. Digest is the content address the viewed
// set is keyed by - the same one internal/diff computes, passed in rather than recomputed
// so the CLI and the console mark the same hunk.
type Hunk struct {
	Header string
	Lines  []string
	Digest string
}

// File is one changed file. Facts are the annotation lines ALREADY RENDERED by the caller,
// because `magus diff` owns that vocabulary and two renderings of "12 files reference its
// widest changed symbol" would drift.
type File struct {
	Path      string
	Generated bool
	Facts     []string
	Hunks     []Hunk
}

// Input is the changeset the viewer opens on. Files arrive in reading order and are never
// re-sorted here - types.Diff.SortForReading is the one definition of that order.
type Input struct {
	Files    []File
	Unranked bool
	// Viewed carries the digests already marked read, from the session or the local store.
	Viewed      []string
	Comments    []types.DiffComment
	Suggestions []types.DiffSuggestion
	// Unfolded starts with generated files expanded, which is what --generated asks for.
	Unfolded bool
	// Link decorates a path for display (an OSC 8 hyperlink). Nil renders it plain.
	Link func(string) string
}

// RowKind says what one visible row is, so the renderer can mark the cursor and the tests
// can assert structure rather than string-match a frame.
type RowKind int

const (
	// RowFile is a file heading: the path and its hunk counts.
	RowFile RowKind = iota
	// RowFact is one annotation line under a heading.
	RowFact
	// RowFold stands in for the hunks of a folded generated file.
	RowFold
	// RowHunk is a hunk heading, carrying the viewed mark.
	RowHunk
	// RowLine is one line of a hunk body.
	RowLine
	// RowComment is a remark anchored to the hunk above it.
	RowComment
	// RowSuggestion is an agent asking for attention. It is displayed and never applied.
	RowSuggestion
	// RowBlank separates one file from the next. Nothing selects it.
	RowBlank
)

// Row is one line of the changeset. Hunk is -1 outside a hunk.
type Row struct {
	Kind RowKind
	File int
	Hunk int
	Text string
	// Emph is which PART of Text changed, in BYTES of Text, on a RowLine that could be paired
	// with its counterpart. The zero span means there is nothing to draw harder than the rest -
	// the line has no partner, or the whole of it changed and the row colour already says so.
	Emph diff.Span
}

// Model is the navigation, fold and progress state machine.
type Model struct {
	files    []File
	link     func(string) string
	unranked bool

	unfolded bool
	viewed   map[string]bool
	comments map[hunkRef][]types.DiffComment
	suggests map[hunkRef][]types.DiffSuggestion

	// file and hunk are where the HUMAN is. hunk is -1 on a file heading.
	file, hunk int
	// cursorRow is where that lands in rows, recomputed by every rebuild.
	cursorRow int
	rows      []Row

	top, height int

	overview   bool
	overCursor int
}

// hunkRef addresses a hunk the way a comment does: by path and index within the file.
type hunkRef struct {
	path string
	hunk int
}

// New builds the model and composes its first row list.
func New(in Input) *Model {
	m := &Model{
		files:    in.Files,
		link:     in.Link,
		unranked: in.Unranked,
		unfolded: in.Unfolded,
		viewed:   make(map[string]bool, len(in.Viewed)),
		comments: map[hunkRef][]types.DiffComment{},
		suggests: map[hunkRef][]types.DiffSuggestion{},
		hunk:     -1,
		height:   1,
	}
	if m.link == nil {
		m.link = func(p string) string { return p }
	}
	for _, d := range in.Viewed {
		m.viewed[d] = true
	}
	for _, c := range in.Comments {
		k := hunkRef{path: c.Path, hunk: c.Hunk}
		m.comments[k] = append(m.comments[k], c)
	}
	for _, s := range in.Suggestions {
		// Answered suggestions are history, not an affordance: showing a declined one again
		// is the repetition DiffSuggestion.Declined exists to stop.
		if s.Accepted || s.Declined {
			continue
		}
		k := hunkRef{path: s.Path, hunk: s.Hunk}
		m.suggests[k] = append(m.suggests[k], s)
	}
	m.rebuild()
	return m
}

// Rows returns every visible row, cursor included. The renderer windows it.
func (m *Model) Rows() []Row { return m.rows }

// CursorRow is the index into Rows of the row the cursor marks, or -1 when there is none.
func (m *Model) CursorRow() int { return m.cursorRow }

// Top is the first row of the viewport.
func (m *Model) Top() int { return m.top }

// Height is how many rows the viewport shows.
func (m *Model) Height() int { return m.height }

// Unranked reports that there was no ranking key, so the order is path order.
func (m *Model) Unranked() bool { return m.unranked }

// Unfolded reports whether generated files are showing their hunks.
func (m *Model) Unfolded() bool { return m.unfolded }

// Overview reports whether the file-list overview is open.
func (m *Model) Overview() bool { return m.overview }

// OverviewCursor is the highlighted file in the overview.
func (m *Model) OverviewCursor() int { return m.overCursor }

// Cursor is where the human is looking, in the shape the session takes.
func (m *Model) Cursor() types.DiffCursor {
	if len(m.files) == 0 {
		return types.DiffCursor{Hunk: -1}
	}
	return types.DiffCursor{Path: m.files[m.file].Path, Hunk: m.hunk}
}

// Resize sets the viewport height and keeps the cursor in view.
func (m *Model) Resize(h int) {
	if h < 1 {
		h = 1
	}
	m.height = h
	m.follow()
}

// Scroll moves the viewport by n rows, clamped at both ends. The cursor does not move: a
// reader looking around should not lose their place.
func (m *Model) Scroll(n int) {
	m.top = clamp(m.top+n, 0, m.maxTop())
}

// Page scrolls a whole viewport in either direction.
func (m *Model) Page(dir int) { m.Scroll(dir * m.height) }

// NextHunk moves to the next hunk shown anywhere below the cursor, crossing file
// boundaries and skipping folded files. Reports whether it moved.
func (m *Model) NextHunk() bool {
	f, h := m.file, m.hunk
	for f < len(m.files) {
		if m.expanded(f) && h+1 < len(m.files[f].Hunks) {
			m.setCursor(f, h+1)
			return true
		}
		f, h = f+1, -1
	}
	return false
}

// PrevHunk is NextHunk's mirror.
func (m *Model) PrevHunk() bool {
	if m.expanded(m.file) && m.hunk > 0 {
		m.setCursor(m.file, m.hunk-1)
		return true
	}
	for f := m.file - 1; f >= 0; f-- {
		if m.expanded(f) && len(m.files[f].Hunks) > 0 {
			m.setCursor(f, len(m.files[f].Hunks)-1)
			return true
		}
	}
	return false
}

// NextFile moves to the next file's heading.
func (m *Model) NextFile() bool {
	if m.file+1 >= len(m.files) {
		return false
	}
	m.setCursor(m.file+1, -1)
	return true
}

// PrevFile moves to the previous file's heading.
func (m *Model) PrevFile() bool {
	if m.file <= 0 {
		return false
	}
	m.setCursor(m.file-1, -1)
	return true
}

// ToggleViewed flips the read mark on the hunk under the cursor and reports what changed,
// so the caller can tell the session. ok is false on a file heading or a hunk with no
// digest - there is nothing to key a mark by.
func (m *Model) ToggleViewed() (digest string, on bool, ok bool) {
	if m.hunk < 0 || len(m.files) == 0 {
		return "", false, false
	}
	hunks := m.files[m.file].Hunks
	if m.hunk >= len(hunks) {
		return "", false, false
	}
	d := hunks[m.hunk].Digest
	if d == "" {
		return "", false, false
	}
	on = !m.viewed[d]
	m.viewed[d] = on
	m.rebuild()
	return d, on, true
}

// Viewed reports whether a digest is marked read.
func (m *Model) Viewed(digest string) bool { return m.viewed[digest] }

// ToggleGenerated folds or unfolds every generated file at once, and recomputes the rows.
// A cursor sitting inside a file that just folded retreats to its heading rather than
// pointing at a row that no longer exists.
func (m *Model) ToggleGenerated() {
	m.unfolded = !m.unfolded
	if len(m.files) > 0 && !m.expanded(m.file) {
		m.hunk = -1
	}
	m.rebuild()
}

// ToggleOverview opens or closes the changeset overview. Opening it starts on the file the
// cursor is in; closing it leaves the cursor exactly where it was.
func (m *Model) ToggleOverview() {
	m.overview = !m.overview
	if m.overview {
		m.overCursor = m.file
	}
}

// OverviewMove walks the overview's file list, clamped at both ends.
func (m *Model) OverviewMove(d int) {
	if len(m.files) == 0 {
		return
	}
	m.overCursor = clamp(m.overCursor+d, 0, len(m.files)-1)
}

// OverviewEnter jumps to the highlighted file and closes the overview.
func (m *Model) OverviewEnter() {
	if len(m.files) == 0 {
		m.overview = false
		return
	}
	m.overview = false
	m.setCursor(m.overCursor, -1)
}

// OverviewRows renders the file list: what each file costs to read and how much of it is
// already read.
func (m *Model) OverviewRows() []string {
	out := make([]string, 0, len(m.files))
	for i := range m.files {
		f := &m.files[i]
		n, read := len(f.Hunks), m.readCount(i)
		line := fmt.Sprintf("%s  %d %s, %d read", m.link(f.Path), n, plural(n, "hunk", "hunks"), read)
		if f.Generated {
			line += ", generated"
		}
		out = append(out, line)
	}
	return out
}

// expanded reports whether file i shows its hunks.
func (m *Model) expanded(i int) bool {
	if i < 0 || i >= len(m.files) {
		return false
	}
	return !m.files[i].Generated || m.unfolded
}

// readCount is how many of a file's hunks are marked read.
func (m *Model) readCount(i int) int {
	n := 0
	for _, h := range m.files[i].Hunks {
		if m.viewed[h.Digest] {
			n++
		}
	}
	return n
}

func (m *Model) setCursor(file, hunk int) {
	m.file, m.hunk = file, hunk
	m.locate()
	m.follow()
}

// rebuild composes the row list from scratch. Every state change that can alter what is
// visible goes through here, so there is one definition of the picture.
func (m *Model) rebuild() {
	m.rows = m.rows[:0]
	for i := range m.files {
		f := &m.files[i]
		if i > 0 {
			m.rows = append(m.rows, Row{Kind: RowBlank, File: i, Hunk: -1})
		}
		m.rows = append(m.rows, Row{Kind: RowFile, File: i, Hunk: -1, Text: m.fileLine(i)})
		if !m.expanded(i) {
			n := len(f.Hunks)
			m.rows = append(m.rows, Row{Kind: RowFold, File: i, Hunk: -1,
				Text: fmt.Sprintf("  %d %s folded - a target rewrites this, so the source edit is what to read",
					n, plural(n, "hunk", "hunks"))})
			continue
		}
		for _, fact := range f.Facts {
			m.rows = append(m.rows, Row{Kind: RowFact, File: i, Hunk: -1, Text: "  " + fact})
		}
		for hi := range f.Hunks {
			h := &f.Hunks[hi]
			mark := "[ ]"
			if m.viewed[h.Digest] {
				mark = "[x]"
			}
			m.rows = append(m.rows, Row{Kind: RowHunk, File: i, Hunk: hi, Text: mark + " " + h.Header})
			emph := lineEmphasis(h.Lines)
			for li, l := range h.Lines {
				m.rows = append(m.rows, Row{Kind: RowLine, File: i, Hunk: hi, Text: l, Emph: emph[li]})
			}
			m.rows = append(m.rows, m.talkRows(i, hi)...)
		}
	}
	m.locate()
	m.follow()
}

// talkRows are the comments and pending suggestions anchored to one hunk.
func (m *Model) talkRows(file, hunk int) []Row {
	k := hunkRef{path: m.files[file].Path, hunk: hunk}
	var out []Row
	for _, c := range m.comments[k] {
		who := string(c.Author)
		if c.AgentName != "" {
			who += " " + c.AgentName
		}
		if c.Resolved {
			who += ", resolved"
		}
		for j, line := range strings.Split(c.Body, "\n") {
			text := "  | " + line
			if j == 0 {
				text = fmt.Sprintf("  | %s: %s", who, line)
			}
			out = append(out, Row{Kind: RowComment, File: file, Hunk: hunk, Text: text})
		}
	}
	for _, s := range m.suggests[k] {
		out = append(out, Row{Kind: RowSuggestion, File: file, Hunk: hunk,
			Text: "  > SUGGESTION: " + s.Reason})
	}
	return out
}

// lineEmphasis reports, per line of one hunk, which part of it changed.
//
// A line diff only says the line is different, which on a rename or a changed argument leaves
// the reader to find the difference by eye across two nearly identical rows. This is what lets
// the renderer draw the changed part harder than the rest.
//
// The pairing is the state machine console/src/console/diff/main.ts runs before it paints: each
// run of removed lines against the run of added lines that follows it. The two surfaces have to
// emphasise the same bytes, or one changeset reads as two. diff.PairForEmphasis is what refuses
// a run whose halves are different lengths, so a rewrite is emphasised and an insertion is left
// alone rather than paired with whatever line happens to sit above it.
//
// The spans returned index the RAW line, marker byte included, because that is what the renderer
// slices. diff.Emphasize is fed the text AFTER the marker, so a '-' and a '+' are never
// themselves counted as the difference between two lines.
func lineEmphasis(lines []string) []diff.Span {
	out := make([]diff.Span, len(lines))
	var dels, adds []int
	flush := func() {
		for _, pair := range diff.PairForEmphasis(dels, adds) {
			d, a := pair[0], pair[1]
			before, after := diff.Emphasize(lines[d][1:], lines[a][1:])
			out[d], out[a] = shiftPastMarker(before), shiftPastMarker(after)
		}
		dels, adds = nil, nil
	}
	for i, l := range lines {
		switch {
		case isDel(l) && len(adds) == 0:
			dels = append(dels, i)
		case isAdd(l) && len(dels) > 0:
			adds = append(adds, i)
		default:
			flush()
			if isDel(l) {
				dels = append(dels, i)
			}
		}
	}
	flush()
	return out
}

// shiftPastMarker moves a span computed on the text after the marker back onto the raw line.
func shiftPastMarker(s diff.Span) diff.Span {
	if s.Empty() {
		return diff.Span{}
	}
	return diff.Span{Start: s.Start + 1, End: s.End + 1}
}

func isDel(line string) bool { return strings.HasPrefix(line, "-") }

func isAdd(line string) bool { return strings.HasPrefix(line, "+") }

func (m *Model) fileLine(i int) string {
	f := &m.files[i]
	n := len(f.Hunks)
	line := fmt.Sprintf("%s  %d %s, %d read", m.link(f.Path), n, plural(n, "hunk", "hunks"), m.readCount(i))
	if f.Generated {
		line += ", generated"
	}
	return line
}

// locate finds the row the cursor marks. A cursor with nothing to point at (an empty
// changeset) reports -1 rather than 0, so the renderer marks no row at all.
func (m *Model) locate() {
	m.cursorRow = -1
	for i, r := range m.rows {
		if r.File != m.file {
			continue
		}
		if m.hunk < 0 && r.Kind == RowFile {
			m.cursorRow = i
			return
		}
		if m.hunk >= 0 && r.Kind == RowHunk && r.Hunk == m.hunk {
			m.cursorRow = i
			return
		}
	}
}

// follow scrolls the viewport the least it can to keep the cursor row on screen.
func (m *Model) follow() {
	m.top = clamp(m.top, 0, m.maxTop())
	if m.cursorRow < 0 {
		return
	}
	if m.cursorRow < m.top {
		m.top = m.cursorRow
		return
	}
	if m.cursorRow >= m.top+m.height {
		m.top = m.cursorRow - m.height + 1
	}
}

// maxTop is the furthest the viewport may scroll: far enough to show the last row, never
// past it into blank space.
func (m *Model) maxTop() int {
	if n := len(m.rows) - m.height; n > 0 {
		return n
	}
	return 0
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
