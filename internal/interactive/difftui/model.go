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
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/types"
)

// Hunk is one @@ section as the viewer shows it. Digest is the content address the viewed
// set is keyed by - the same one internal/diff computes, passed in rather than recomputed
// so the CLI and the console mark the same hunk.
type Hunk struct {
	// NewStart is the hunk's first line on the new side, and Declaration is the enclosing
	// declaration git named in its header. Together they are what the heading row says, in place
	// of the raw @@ coordinates: where the reader is, and what they are inside of.
	NewStart    int
	Declaration string
	// Index is the hunk's position in the PATCH, which is the coordinate a comment and a
	// suggestion are anchored by (see changeset.Hunk.Index). Carried rather than taken from the
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
	Emph []changeset.Span
}

// File is one changed file. Facts are the annotation lines ALREADY RENDERED by the caller,
// because `magus diff` owns that vocabulary and two renderings of "12 files reference its
// widest changed symbol" would drift.
type File struct {
	Path string
	// Settled is a file a receipt covers at exactly its current content: read, and unmoved since.
	//
	// Folded by default for the same reason Generated is - it is not what the reader is here for -
	// but for a different reason, so it is a separate flag and a separate key. A generated file is
	// a machine's restatement of an edit made elsewhere; a settled file is one this reader already
	// weighed. Conflating them would fold a colleague's unreviewed generated file and a reader's
	// own finished work under one word.
	Settled   bool
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
	// Threads are the remarks already on the host's review, with Hunk resolved by
	// changeset.PlaceThreads. A thread whose line this changeset does not contain (Hunk < 0) renders
	// under the file heading rather than being dropped: a colleague said it, and a viewer that
	// silently withheld it would be telling the reader nobody had.
	Threads []types.ReviewThread
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
	Emph changeset.Span
}

// Model is the navigation, fold and progress state machine.
type Model struct {
	files    []File
	link     func(string) string
	unranked bool

	unfolded bool
	// unsettled shows the already-reviewed files that are folded away by default.
	unsettled bool
	viewed    map[string]bool
	comments  map[hunkRef][]types.DiffComment
	suggests  map[hunkRef][]types.DiffSuggestion
	// threads is the host's remarks by hunk; unplaced holds, per path, the ones whose line this
	// changeset does not contain.
	threads  map[hunkRef][]types.ReviewThread
	unplaced map[string][]types.ReviewThread

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
		threads:  map[hunkRef][]types.ReviewThread{},
		unplaced: map[string][]types.ReviewThread{},
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
	for _, t := range in.Threads {
		if t.Hunk < 0 {
			m.unplaced[t.Path] = append(m.unplaced[t.Path], t)
			continue
		}
		k := hunkRef{path: t.Path, hunk: t.Hunk}
		m.threads[k] = append(m.threads[k], t)
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

// Unsettled reports whether already-reviewed files are being shown.
func (m *Model) Unsettled() bool { return m.unsettled }

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

// ToggleSettled folds or unfolds every already-reviewed file at once.
//
// This is what makes a second pass cost only the second pass. A reviewer who asked for changes
// comes back to a changeset where most files are exactly what they already read, and nothing
// distinguishes those from the ones that moved - so they re-read everything, find the same things,
// and learn that re-reviewing is not worth doing carefully.
//
// Folded by DEFAULT, and the count is always stated, because a hidden file nobody was told about
// is the one failure this surface cannot have.
func (m *Model) ToggleSettled() {
	m.unsettled = !m.unsettled
	if len(m.files) > 0 && !m.expanded(m.file) {
		m.hunk = -1
	}
	m.rebuild()
}

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

// hunkHeading is what a hunk's row says, in place of the raw @@ line.
//
// The @@ coordinates are WIRE SYNTAX. "-743,6 +762,14" is four numbers a reader has to decode to
// learn one thing they wanted (where am I) and three they did not. What is useful in that line is
// the position and the declaration git named, so those are what it says.
//
// A hunk git could name no declaration for keeps the position alone rather than inventing one.
func hunkHeading(h *Hunk) string {
	at := fmt.Sprintf("line %d", h.NewStart)
	if h.Declaration == "" {
		return at
	}
	return at + "  " + h.Declaration
}

// foldReason says WHY a file is folded, because the two reasons send the reader to different
// places: a generated file's source edit is elsewhere, and a settled file has already been read.
//
// Generated wins where both apply. A generated file is not worth reading whether or not this
// reader got to it, so its reason is the more useful of the two.
func foldReason(f *File) string {
	if f.Generated {
		return "a target rewrites this, so the source edit is what to read"
	}
	return "you read this already and it has not changed since; press n to show it"
}

// expanded reports whether file i shows its hunks.
func (m *Model) expanded(i int) bool {
	if i < 0 || i >= len(m.files) {
		return false
	}
	if m.files[i].Generated && !m.unfolded {
		return false
	}
	return !m.files[i].Settled || m.unsettled
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
	// The paths this pass actually draws a body for. A folded file is NOT one of them: its hunks
	// are stood in for by a single row, so a remark anchored inside it has nowhere to sit here
	// either.
	shown := make(map[string]bool, len(m.files))
	for i := range m.files {
		f := &m.files[i]
		if i > 0 {
			m.rows = append(m.rows, Row{Kind: RowBlank, File: i, Hunk: -1})
		}
		m.rows = append(m.rows, Row{Kind: RowFile, File: i, Hunk: -1, Text: m.fileLine(i)})
		if !m.expanded(i) {
			n := len(f.Hunks)
			m.rows = append(m.rows, Row{Kind: RowFold, File: i, Hunk: -1,
				Text: fmt.Sprintf("  %d %s folded - %s", n, plural(n, "hunk", "hunks"), foldReason(f))})
			continue
		}
		shown[f.Path] = true
		for _, fact := range f.Facts {
			m.rows = append(m.rows, Row{Kind: RowFact, File: i, Hunk: -1, Text: "  " + fact})
		}
		// Threads whose line this changeset no longer contains, under the heading rather than
		// dropped. The line moved after a colleague wrote; what they said still stands.
		for _, t := range m.unplaced[f.Path] {
			m.rows = append(m.rows, threadRows(t, i, -1)...)
		}
		for hi := range f.Hunks {
			h := &f.Hunks[hi]
			mark := "[ ]"
			if m.viewed[h.Digest] {
				mark = "[x]"
			}
			m.rows = append(m.rows, Row{Kind: RowHunk, File: i, Hunk: hi, Text: mark + " " + hunkHeading(h)})
			for li, l := range h.Lines {
				var emph changeset.Span
				if li < len(h.Emph) {
					emph = h.Emph[li]
				}
				m.rows = append(m.rows, Row{Kind: RowLine, File: i, Hunk: hi, Text: l, Emph: emph})
			}
			m.rows = append(m.rows, m.talkRows(i, hi, h)...)
		}
	}
	m.rows = append(m.rows, m.elsewhereRows(shown)...)
	m.locate()
	m.follow()
}

// elsewhereRows are the remarks this pass has nowhere to put: on a file the changeset does not
// contain, or on one folded away.
//
// Listed rather than dropped, which is the whole rule the placement follows - "your colleague
// said nothing" is the one thing a review surface must never say by accident. A pull request
// covers commits a working diff does not, so a thread landing outside it is ordinary rather than
// exceptional, and until this existed the terminal viewer discarded every one of them in silence
// while the console listed them.
//
// Sorted, because they are gathered from maps and an unsorted read would reorder the tail of the
// changeset between frames.
func (m *Model) elsewhereRows(shown map[string]bool) []Row {
	var out []types.ReviewThread
	for path, ts := range m.unplaced {
		if !shown[path] {
			out = append(out, ts...)
		}
	}
	for k, ts := range m.threads {
		if !shown[k.path] {
			out = append(out, ts...)
		}
	}
	if len(out) == 0 {
		return nil
	}
	slices.SortFunc(out, func(a, b types.ReviewThread) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Line, b.Line); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	rows := []Row{
		{Kind: RowBlank, File: -1, Hunk: -1},
		{Kind: RowFile, File: -1, Hunk: -1, Text: fmt.Sprintf("said on the review, elsewhere (%d)", len(out))},
	}
	for _, t := range out {
		rows = append(rows, Row{Kind: RowFact, File: -1, Hunk: -1,
			Text: fmt.Sprintf("  %s:%d", t.Path, t.Line)})
		rows = append(rows, threadRows(t, -1, -1)...)
	}
	return rows
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
	// The host's threads first. What a colleague already said is context for the remark you are
	// about to write, not a footnote to it - the same order the console renders.
	for _, t := range m.threads[k] {
		out = append(out, threadRows(t, file, row)...)
	}
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

// threadRows renders one remark from the host's review, wrapped the way a comment is.
//
// It says "on the review" rather than naming the author alone, because the reader has to be
// able to tell what the world has already seen from what is still theirs to send. The console
// draws the same distinction with a colour it cannot use here.
func threadRows(t types.ReviewThread, file, hunk int) []Row {
	who := t.Author
	if who == "" {
		who = "review"
	}
	lines := strings.Split(t.Body, "\n")
	out := make([]Row, 0, len(lines))
	for j, line := range lines {
		text := "  | " + line
		if j == 0 {
			text = fmt.Sprintf("  | %s, on the review: %s", who, line)
		}
		out = append(out, Row{Kind: RowComment, File: file, Hunk: hunk, Text: text})
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
