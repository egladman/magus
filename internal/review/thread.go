package review

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/types"
)

// threadCallerLimit caps the callers named per symbol. The cut is announced in the line it
// shortens, because a truncated list that does not say so reads as the whole answer.
const threadCallerLimit = 5

// ThreadInput is everything a thread record is read from. [NewThreadInput] assembles it, so the
// CLI, the HTTP route and the MCP tool cannot each assemble a different one.
type ThreadInput struct {
	// Changeset is the annotated changeset the thread is read against. It may be the zero value:
	// a thread outlives the working tree it was written on, and the record then carries the
	// thread and the host's own hunk text alone.
	Changeset types.Diff
	// Hunks are the patch's hunks, which give the text of the hunk a comment sits in and place
	// each comment onto it. Nil leaves every comment unplaced.
	Hunks []changeset.FileHunks
	// Comments are the review's comments, flat as the host reports them, oldest first.
	Comments []types.ReviewComment
	// Anchors are the note anchors the changeset touches; the record keeps the ones that name
	// the thread's file or the symbols changed in it.
	Anchors []AnchorHit
	// AnchorsUnread says why Anchors was not read, when it was not: a transport with no notes
	// store to join against. The record then lists it among what could not be measured, because
	// an empty Anchors would otherwise read as "no note anchors this file".
	AnchorsUnread string
}

// ThreadParts are the pieces of a record's input a transport supplies. Each transport reads the
// forge and the workspace its own way; what it does with them is [NewThreadInput]'s.
type ThreadParts struct {
	// Patch is the unified patch the thread is read against. Empty is fine: a review outlives
	// the tree it was written on.
	Patch string
	// Comments are the review's comments, as the host reports them.
	Comments []types.ReviewComment
	// Annotate computes the annotated changeset for the paths the patch touches. It is called
	// only when the patch has hunks, and with no bound of its own: only a forge call is worth
	// cutting short. The record never reads the reading order, so an Annotate that can skip it
	// should.
	Annotate func(ctx context.Context, paths []string) (types.Diff, error)
	// Anchors joins the notes stores against the changeset. Nil is a transport with no notes
	// store wired, and the record then names note anchors among what it could not measure.
	Anchors func(ctx context.Context, rev types.Diff) ([]AnchorHit, error)
}

// NewThreadInput assembles a record's input from what a transport read. It returns the error of
// Annotate or Anchors: a misdeclared notes store is a fault to report, not a section to omit.
func NewThreadInput(ctx context.Context, p ThreadParts) (ThreadInput, error) {
	in := ThreadInput{Hunks: changeset.ParseHunks(p.Patch), Comments: p.Comments}
	if len(in.Hunks) > 0 && p.Annotate != nil {
		paths := make([]string, 0, len(in.Hunks))
		for _, f := range in.Hunks {
			if f.Path != "" {
				paths = append(paths, f.Path)
			}
		}
		rev, err := p.Annotate(ctx, paths)
		if err != nil {
			return ThreadInput{}, err
		}
		in.Changeset = rev
	}
	if p.Anchors == nil {
		in.AnchorsUnread = "this server has no notes store wired, so none was joined"
		return in, nil
	}
	hits, err := p.Anchors(ctx, in.Changeset)
	if err != nil {
		return ThreadInput{}, err
	}
	in.Anchors = hits
	return in, nil
}

// ReadThread reads one review thread for a person: the whole exchange oldest first, the code it
// is about, and what the change there reaches.
//
// id may be the thread's first comment or any reply in it. It returns an error wrapping
// [changeset.ErrNoThread] when the id names none.
//
// Everything quoted from the host (comment bodies, authors and hunk text) is escaped of the
// characters a renderer obeys but a reader cannot see.
func ReadThread(in ThreadInput, id string) (types.DiffThread, error) {
	thread, err := changeset.FindThread(changeset.PlaceThreads(in.Hunks, in.Comments), id)
	if err != nil {
		return types.DiffThread{}, err
	}
	head := thread.Head
	file, inChangeset := changedFile(in.Changeset, head.Path)
	out := types.DiffThread{
		ID:          thread.ID(),
		Path:        head.Path,
		Line:        head.Line,
		Outdated:    head.Outdated,
		Comments:    quoted(thread.Comments()),
		Hunk:        threadHunk(in.Hunks, head),
		InChangeset: inChangeset,
		Change:      changeLine(in.Changeset),
		Unmeasured:  threadGaps(in, file, inChangeset),
	}
	if inChangeset {
		out.Project, out.Role, out.Reach, out.Coverage = file.Project, file.Role, file.Reach, file.Coverage
		out.Symbols, out.SymbolsNote = hunkSymbols(file, head)
		out.Notes = fileAnchorLines(in.Anchors, file)
	}
	return out, nil
}

// AttachThreads sets each changed file's Threads from the review's comments, placed onto
// hunks. A thread on a file the changeset does not hold is left out, since a report lists files.
func AttachThreads(rev *types.Diff, hunks []changeset.FileHunks, comments []types.ReviewComment) {
	byPath := map[string][]types.DiffThreadRef{}
	for _, t := range changeset.GroupThreads(changeset.PlaceThreads(hunks, comments)) {
		byPath[t.Head.Path] = append(byPath[t.Head.Path], types.DiffThreadRef{
			ID:       t.ID(),
			Hunk:     t.Head.Hunk,
			Line:     t.Head.Line,
			Comments: 1 + len(t.Replies),
			Outdated: t.Head.Outdated,
		})
	}
	for i := range rev.Files {
		rev.Files[i].Threads = byPath[rev.Files[i].Path]
	}
}

// ThreadRefLine is how a report names one thread beside the code it sits on.
func ThreadRefLine(r types.DiffThreadRef) string {
	line := fmt.Sprintf("thread %s, %d %s", r.ID, r.Comments, plural(r.Comments, "comment", "comments"))
	if r.Outdated {
		line += ", outdated"
	}
	return line
}

// ThreadLines renders a record for the person reading it in a terminal.
func ThreadLines(t types.DiffThread) []string {
	lines := []string{"thread " + t.ID + " on " + threadWhere(t), ""}
	lines = append(lines, "conversation, oldest first, quoted from the review:")
	for _, c := range t.Comments {
		who := c.Author
		if who == "" {
			who = "unknown author"
		}
		lines = append(lines, "  "+who+":")
		for l := range strings.SplitSeq(strings.TrimSpace(c.Body), "\n") {
			lines = append(lines, "    > "+l)
		}
	}

	lines = append(lines, "", t.Hunk.Note+":")
	for _, l := range t.Hunk.Lines {
		lines = append(lines, "    "+l)
	}

	lines = append(lines, "", "what the change reaches:")
	if t.InChangeset {
		lines = append(lines, "  "+fileLine(t))
	}
	for _, s := range symbolLines(t.Symbols) {
		lines = append(lines, "  "+s)
	}
	if t.SymbolsNote != "" {
		lines = append(lines, "  "+t.SymbolsNote)
	}
	for _, n := range t.Notes {
		lines = append(lines, "  note: "+n)
	}
	if t.Change != "" {
		lines = append(lines, "  "+t.Change)
	}
	if len(t.Unmeasured) > 0 {
		lines = append(lines, "", "not measured, so an empty answer above is not a clean one:")
		for _, g := range t.Unmeasured {
			lines = append(lines, "  "+g)
		}
	}
	return lines
}

// changedFile finds path in the changeset. The thread can sit on a file the working tree no
// longer changes, and the record then says so instead of describing some other file.
func changedFile(rev types.Diff, path string) (types.DiffFile, bool) {
	for _, f := range rev.Files {
		if f.Path == path {
			return f, true
		}
	}
	return types.DiffFile{}, false
}

// quoted returns the comments with their author and body escaped.
func quoted(comments []types.ReviewComment) []types.ReviewComment {
	out := make([]types.ReviewComment, len(comments))
	for i, c := range comments {
		c.Author, c.Body, c.DiffHunk = printable(c.Author), printable(c.Body), printable(c.DiffHunk)
		out[i] = c
	}
	return out
}

// threadWhere is path:line for the thread's first comment, saying when the host marked the
// line as gone from the head.
func threadWhere(t types.DiffThread) string {
	where := t.Path
	if t.Path != "" && t.Line > 0 {
		where = fmt.Sprintf("%s:%d", t.Path, t.Line)
	}
	if t.Outdated {
		where += " (outdated: the line no longer exists in the head)"
	}
	return where
}

// threadHunk is the code the thread is about.
//
// The patch's hunk wins while the comment still sits in one: it is the code as the reader sees
// it now. An outdated comment, or one on a file the changeset no longer holds, falls back to the
// host's own copy, the only record of the code it was about. Both can be absent, and then the
// lines are empty and the note says why rather than leaving a heading with nothing under it.
func threadHunk(files []changeset.FileHunks, head types.ReviewComment) types.DiffThreadHunk {
	if head.Hunk >= 0 && !head.Outdated {
		for _, f := range files {
			if f.Path != head.Path {
				continue
			}
			for _, h := range f.Hunks {
				if h.Index != head.Hunk {
					continue
				}
				body := h.Lines
				if h.Display != nil {
					body = h.Display
				}
				return types.DiffThreadHunk{
					Index:  h.Index,
					Source: "patch",
					Lines:  printableLines(append([]string{h.Header}, body...)),
					Note:   fmt.Sprintf("hunk %d of %s, as it stands now", h.Index, head.Path),
				}
			}
		}
	}
	if head.DiffHunk != "" {
		return types.DiffThreadHunk{
			Index:  -1,
			Source: "host",
			Lines:  printableLines(strings.Split(strings.TrimRight(head.DiffHunk, "\n"), "\n")),
			Note:   "the host's copy of the hunk, as it was when the comment was made",
		}
	}
	return types.DiffThreadHunk{Index: -1, Note: "no hunk text: the comment sits outside this changeset and the host sent none"}
}

// hunkSymbols returns the changed symbols in the hunk the thread sits in.
//
// The second result is set when the question has no per-hunk answer, and says why. A comment
// whose hunk is gone cannot be matched to symbols, and listing the whole file's would put
// symbols that were never discussed under the comment; the note says what was left out.
func hunkSymbols(file types.DiffFile, head types.ReviewComment) ([]types.DiffSymbol, string) {
	if len(file.Symbols) == 0 {
		return nil, "No symbol index covers this file, so the symbols changed here are unknown. That is not a finding that there are none."
	}
	if head.Hunk < 0 {
		return nil, fmt.Sprintf("The comment's hunk is not in this changeset, so its symbols cannot be placed. The file's changed symbols are: %s.",
			strings.Join(symbolNames(file.Symbols), ", "))
	}
	var ids []string
	for _, h := range file.Hunks {
		if h.Index == head.Hunk {
			ids = h.Symbols
		}
	}
	var out []types.DiffSymbol
	for _, id := range ids {
		if i := slices.IndexFunc(file.Symbols, func(s types.DiffSymbol) bool { return s.ID == id }); i >= 0 {
			out = append(out, file.Symbols[i])
		}
	}
	if len(out) == 0 {
		return nil, "The change in this hunk is not inside a symbol the index knows."
	}
	return out, ""
}

func symbolNames(syms []types.DiffSymbol) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		out = append(out, "`"+changeset.SymbolName(s)+"`")
	}
	return out
}

// fileLine is the file's role, project, reach and coverage in one line. Facts nobody measured
// are omitted rather than zeroed.
func fileLine(t types.DiffThread) string {
	facts := []string{t.Path}
	if t.Role != "" {
		facts = append(facts, t.Role)
	}
	if t.Project != "" {
		facts = append(facts, "in "+t.Project)
	}
	if t.Reach != nil {
		facts = append(facts, fmt.Sprintf("widest reach %d file(s)", *t.Reach))
	}
	if c := t.Coverage; c != nil {
		facts = append(facts, fmt.Sprintf("%.0f%% of statements covered (%d of %d)", c.Ratio*100, c.Covered, c.Total))
	}
	return strings.Join(facts, "; ")
}

// symbolLines renders one changed symbol per line: its definition, then the facts that say who
// feels it. Facts the index did not record are omitted rather than zeroed.
func symbolLines(syms []types.DiffSymbol) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		facts := []string{}
		if s.Signature != "" {
			facts = append(facts, "defined as `"+s.Signature+"`")
		}
		if s.Change != "" {
			facts = append(facts, "this change: "+s.Change)
		}
		facts = append(facts, fmt.Sprintf("referenced from %d file(s), %d reference(s)", s.FileCount, s.RefCount))
		if len(s.PublicTo) > 0 {
			facts = append(facts, "public to "+strings.Join(s.PublicTo, ", "))
		}
		if s.PublicBeyondWorkspace {
			facts = append(facts, "exported from the module")
		}
		for _, c := range s.Checks {
			facts = append(facts, "conformance: "+c.Message)
		}
		if callers := callerLines(s.PublicThrough); callers != "" {
			facts = append(facts, callers)
		}
		out = append(out, "`"+changeset.SymbolName(s)+"` - "+strings.Join(facts, "; "))
	}
	return out
}

// callerLines names the callers through which a symbol reaches code used outside its package or
// project, nearest chain first.
func callerLines(paths []types.DiffPublicPath) string {
	if len(paths) == 0 {
		return ""
	}
	shown := paths
	if len(shown) > threadCallerLimit {
		shown = shown[:threadCallerLimit]
	}
	names := make([]string, 0, len(shown))
	for _, p := range shown {
		name := p.Qualified
		if name == "" {
			name = p.ID
		}
		if len(p.Via) > 0 {
			name += " via " + strings.Join(p.Via, " -> ")
		}
		names = append(names, "`"+name+"` ("+p.Boundary+")")
	}
	line := "reached through " + strings.Join(names, ", ")
	if dropped := len(paths) - len(shown); dropped > 0 {
		line += fmt.Sprintf(", and %d more", dropped)
	}
	return line
}

// fileAnchorLines keeps the anchor hits that name this file or one of its changed symbols.
func fileAnchorLines(hits []AnchorHit, file types.DiffFile) []string {
	if file.Path == "" {
		return nil
	}
	mine := map[string]bool{file.Path: true}
	for _, s := range file.Symbols {
		mine[s.ID] = true
	}
	var out []string
	for _, h := range hits {
		if mine[h.Matched] {
			out = append(out, h.Line())
		}
	}
	return out
}

// changeLine is the change as a whole, in one sentence.
func changeLine(rev types.Diff) string {
	if len(rev.Files) == 0 {
		return ""
	}
	line := fmt.Sprintf("The change touches %d file(s)", len(rev.Files))
	if len(rev.SeedProjects) > 0 {
		line += " in " + strings.Join(rev.SeedProjects, ", ")
	}
	if n := len(rev.AffectedProjects); n > len(rev.SeedProjects) {
		line += fmt.Sprintf("; %d project(s) rebuild as a result", n)
	}
	return line + "."
}

// threadGaps lists what the record could not say, so each missing field reads as a gap rather
// than as a clean result.
func threadGaps(in ThreadInput, file types.DiffFile, inChangeset bool) []string {
	rev := in.Changeset
	var gaps []string
	if in.AnchorsUnread != "" {
		gaps = append(gaps, "note anchors: "+in.AnchorsUnread)
	}
	switch {
	case len(rev.Files) == 0:
		gaps = append(gaps, "no changeset was read, so nothing is known about the code beyond the host's hunk")
	case !inChangeset:
		gaps = append(gaps, "the thread's file is not in the current changeset, so its symbols, reach and coverage are unknown")
	default:
		if file.Reach == nil {
			gaps = append(gaps, "reach: no symbol index was loaded for this file")
		}
		if file.Coverage == nil {
			gaps = append(gaps, "coverage: no coverage run has been observed for this file")
		}
	}
	if e := rev.ConformanceError; e != nil {
		gaps = append(gaps, "conformance: ["+e.Code+"] "+e.Message)
	}
	for _, u := range rev.Uncovered {
		if u.Project == file.Project && file.Project != "" {
			gaps = append(gaps, "conformance: not checked for `"+u.Project+"` ("+u.Reason.Sentence()+")")
		}
	}
	return append(gaps, rev.Notes...)
}

// printable escapes the characters a renderer obeys but a reader cannot see.
func printable(s string) string {
	safe, _ := changeset.SanitizeBidi(s)
	return safe
}

func printableLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = printable(l)
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
