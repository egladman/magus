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
	// Index is the hunk's position in the PATCH, which is the coordinate a comment and a
	// suggestion are anchored by (see diff.Hunk.Index). Carried rather than taken from the
	// position in Hunks, so a caller that ever hands over a subset cannot silently renumber
	// every anchor in the file.
	Index  int
	Header string
	Lines  []string
	Digest string
	// Emph is, per line of Lines, which part of it changed - as byte offsets into the RAW
	// line, marker included, because that is what the renderer slices. Empty or short is fine
	// and means no emphasis, which is what a caller that does not compute it gets.
	//
	// Passed in rather than derived here, like Digest above and for the same reason: the
	// parser works it out once and both surfaces read the one answer.
	Emph []diff.Span
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
	// the line has no partner, or the whole of it changed and the row color already says so.
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
//
// The slice ALIASES the model's own, which rebuild refills in place. A cursor move does not
// rebuild, but a fold or a read mark does, and a row list that grew REALLOCATES - so an earlier
// return value is left pointing at whichever backing array it was handed, stale rather than
// live. Read it and drop it rather than holding it. Copying instead would allocate the whole
// changeset on each of the frames a keypress draws, which is the cost the reuse exists to avoid.
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
//
// Hunk is the hunk's index in the PATCH (Hunk.Index), which is the coordinate the session
// addresses talk by - the same one talkRows joins comments on. It is NOT the row position the
// cursor walks: those coincide only while the viewer holds every hunk of every file, so
// publishing the position would key the shared cursor by something no other client can resolve.
func (m *Model) Cursor() types.DiffCursor {
	if len(m.files) == 0 {
		return types.DiffCursor{Hunk: -1}
	}
	f := &m.files[m.file]
	if m.hunk < 0 || m.hunk >= len(f.Hunks) {
		return types.DiffCursor{Path: f.Path, Hunk: -1}
	}
	return types.DiffCursor{Path: f.Path, Hunk: f.Hunks[m.hunk].Index}
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

// NextFile moves to the next file's heading. Reports whether it moved.
func (m *Model) NextFile() bool {
	if m.file+1 >= len(m.files) {
		return false
	}
	m.setCursor(m.file+1, -1)
	return true
}

// PrevFile moves to the previous file's heading. Reports whether it moved.
func (m *Model) PrevFile() bool {
	if m.file <= 0 {
		return false
	}
	m.setCursor(m.file-1, -1)
	return true
}

// ViewedChange is one flip of a read mark: the hunk it was made on, and which way it went.
type ViewedChange struct {
	Digest string
	On     bool
}

// ToggleViewed flips the read mark on the hunk under the cursor and reports what changed,
// so the caller can tell the session. ok is false on a file heading or a hunk with no
// digest - there is nothing to key a mark by.
func (m *Model) ToggleViewed() (change ViewedChange, ok bool) {
	if m.hunk < 0 || len(m.files) == 0 {
		return ViewedChange{}, false
	}
	hunks := m.files[m.file].Hunks
	if m.hunk >= len(hunks) {
		return ViewedChange{}, false
	}
	d := hunks[m.hunk].Digest
	if d == "" {
		return ViewedChange{}, false
	}
	on := !m.viewed[d]
	m.viewed[d] = on
	m.rebuild()
	return ViewedChange{Digest: d, On: on}, true
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

// OverviewRow is one file in the changeset overview. Rendered is the line the frame prints;
// the fields beside it are what that line SAYS, so a caller reads the answer rather than
// matching text out of it again.
type OverviewRow struct {
	// Path is undecorated. The link, when there is one, is in Rendered.
	Path string
	// HunkCount rather than Hunks: File.Hunks in this same package is the hunks THEMSELVES, and
	// one name for two shapes reads as a copy of the slice at every use.
	HunkCount int
	Read      int
	Generated bool
	Rendered  string
}

// OverviewRows is the file list: what each file costs to read and how much of it is already
// read.
func (m *Model) OverviewRows() []OverviewRow {
	out := make([]OverviewRow, 0, len(m.files))
	for i := range m.files {
		f := &m.files[i]
		n, read := len(f.Hunks), m.readCount(i)
		line := fmt.Sprintf("%s  %d %s, %d read", m.link(f.Path), n, plural(n, "hunk", "hunks"), read)
		if f.Generated {
			line += ", generated"
		}
		out = append(out, OverviewRow{
			Path: f.Path, HunkCount: n, Read: read, Generated: f.Generated, Rendered: line,
		})
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
			for li, l := range h.Lines {
				var emph diff.Span
				if li < len(h.Emph) {
					emph = h.Emph[li]
				}
				m.rows = append(m.rows, Row{Kind: RowLine, File: i, Hunk: hi, Text: l, Emph: emph})
			}
			m.rows = append(m.rows, m.talkRows(i, hi, h)...)
		}
	}
	m.locate()
	m.follow()
}

// talkRows are the comments and pending suggestions anchored to one hunk.
//
// Two coordinates, and they are not the same one: talk is addressed by the hunk's position in
// the PATCH (h.Index, which is what the MCP surface validates a comment against), while a row
// is addressed by where the hunk sits in this file's list, because that is what the cursor
// walks. They coincide while the viewer is handed every hunk of every file.
func (m *Model) talkRows(file, row int, h *Hunk) []Row {
	k := hunkRef{path: m.files[file].Path, hunk: h.Index}
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
			out = append(out, Row{Kind: RowComment, File: file, Hunk: row, Text: text})
		}
	}
	for _, s := range m.suggests[k] {
		out = append(out, Row{Kind: RowSuggestion, File: file, Hunk: row,
			Text: "  > SUGGESTION: " + s.Reason})
	}
	return out
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
