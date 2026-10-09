package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive/difftui"
	"github.com/egladman/magus/types"
)

// printDiffOrder writes the reading order under the file summary: each group, each step with
// its hunks and the sentence that placed them, then the completeness line. Nothing for a diff
// with no order, because the note it carries says why.
func printDiffOrder(w io.Writer, rev types.Diff, showGenerated bool) {
	for _, line := range diffOrderLines(rev, showGenerated) {
		fmt.Fprintln(w, line)
	}
}

func diffOrderLines(rev types.Diff, showGenerated bool) []string {
	order := rev.Order
	if order == nil {
		return nil
	}
	ranges := hunkRanges(rev)
	lines := []string{"", "reading order"}
	for gi, g := range order.Groups {
		lines = append(lines, "", fmt.Sprintf("group %d of %d: %s", gi+1, len(order.Groups), diffGroupTitle(g)))
		if g.Kind == types.DiffGroupGenerated && !showGenerated {
			lines = append(lines, "  folded: a target rewrites these; show them with --generated")
			continue
		}
		for _, s := range g.Steps {
			lines = append(lines, fmt.Sprintf("  step %d", s.Number))
			for _, h := range s.Hunks {
				at := ranges[h.Hunk.Path+"#"+fmt.Sprint(h.Hunk.Index)]
				if at == "" {
					at = h.Hunk.Path
				}
				head := "    " + at
				if h.Label != "" {
					head += "  " + h.Label
				}
				lines = append(lines, head, "        "+h.Why.Text)
			}
		}
	}
	return append(lines, "", diffOrderCountLine(order.Count))
}

func diffGroupTitle(g types.DiffGroup) string {
	title := string(g.Kind)
	if g.Label != "" {
		title += " around " + g.Label
	}
	return fmt.Sprintf("%s (%s)", title, plural(g.Hunks, "1 hunk", fmt.Sprintf("%d hunks", g.Hunks)))
}

// diffOrderCountLine states what was placed, and names every hunk or file the order could not
// account for, so a reader never takes a short list for a whole one.
func diffOrderCountLine(c types.DiffOrderCount) string {
	line := fmt.Sprintf("%d hunks, %d placed", c.Hunks, c.Placed)
	if len(c.Repeated) > 0 {
		line += fmt.Sprintf("; %d placed more than once: %s", len(c.Repeated), refList(c.Repeated))
	}
	if len(c.Missing) > 0 {
		line += fmt.Sprintf("; %d not placed: %s", len(c.Missing), refList(c.Missing))
	}
	if len(c.Bare) > 0 {
		line += fmt.Sprintf("; %d files with no hunk to show: %s", len(c.Bare), strings.Join(c.Bare, ", "))
	}
	return line
}

func refList(refs []types.DiffHunkRef) string {
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		names = append(names, fmt.Sprintf("%s#%d", r.Path, r.Index))
	}
	return strings.Join(names, ", ")
}

// hunkRanges names each hunk's lines of the new file, keyed "path#index".
func hunkRanges(rev types.Diff) map[string]string {
	out := map[string]string{}
	for _, f := range rev.Files {
		for _, h := range f.Hunks {
			out[fmt.Sprintf("%s#%d", f.Path, h.Index)] = newRange(f.Path, h.NewStart, h.NewCount)
		}
	}
	return out
}

// newRange is path:first-last, or path:line (deleted) for a hunk that leaves no line behind.
func newRange(path string, start, count int) string {
	switch {
	case count == 0:
		return fmt.Sprintf("%s:%d (deleted)", path, start)
	case count == 1:
		return fmt.Sprintf("%s:%d", path, start)
	}
	return fmt.Sprintf("%s:%d-%d", path, start, start+count-1)
}

// diffOrderTUIFiles projects the reading order onto the viewer: one file per step and path, in
// step order, each hunk's Why sentence as a fact. Every hunk lands in exactly one file, so the
// receipts earnedSync mints per path still add up. A diff with no order, or one whose order
// does not account for every parsed hunk, keeps today's file-by-file projection.
func diffOrderTUIFiles(rev types.Diff, parsed []changeset.FileHunks) []difftui.File {
	if rev.Order == nil {
		return diffTUIFiles(rev, parsed)
	}
	byKey := map[string]changeset.Hunk{}
	total := 0
	for _, f := range parsed {
		for _, h := range f.Hunks {
			byKey[fmt.Sprintf("%s#%d", f.Path, h.Index)] = h
			total++
		}
	}
	files := map[string]types.DiffFile{}
	for _, f := range rev.Files {
		files[f.Path] = f
	}

	var out []difftui.File
	shown := map[string]bool{}
	placed := map[string]bool{}
	for _, g := range rev.Order.Groups {
		for _, s := range g.Steps {
			current := map[string]int{}
			for _, sh := range s.Hunks {
				key := fmt.Sprintf("%s#%d", sh.Hunk.Path, sh.Hunk.Index)
				h, ok := byKey[key]
				if !ok || placed[key] {
					return diffTUIFiles(rev, parsed)
				}
				placed[key] = true
				at, seen := current[sh.Hunk.Path]
				if !seen {
					src := files[sh.Hunk.Path]
					piece := difftui.File{
						Path:      sh.Hunk.Path,
						Settled:   src.ReadState == types.DiffReadRead,
						Generated: g.Kind == types.DiffGroupGenerated || src.Generated(),
						Facts:     []string{fmt.Sprintf("step %d of %d, %s", s.Number, diffOrderSteps(rev.Order), diffGroupTitle(g))},
					}
					if !shown[sh.Hunk.Path] {
						piece.Facts = append(piece.Facts, diffFileFacts(src)...)
						shown[sh.Hunk.Path] = true
					}
					out = append(out, piece)
					at = len(out) - 1
					current[sh.Hunk.Path] = at
				}
				fact := sh.Why.Text
				if sh.Label != "" {
					fact = sh.Label + ": " + fact
				}
				out[at].Facts = append(out[at].Facts, fact)
				out[at].Hunks = append(out[at].Hunks, difftui.Hunk{
					Index: h.Index, Header: h.Header, Lines: displayOr(h), Digest: h.Digest,
					NewStart: h.NewStart, Declaration: h.Declaration,
					Emph: changeset.RawLineEmphasis(h),
				})
			}
		}
	}
	if len(placed) != total {
		return diffTUIFiles(rev, parsed)
	}
	// A file with no hunk (a rename, a mode change, a binary) has no step; it follows the order
	// so the viewer still lists it.
	for _, f := range rev.Files {
		if !shown[f.Path] {
			out = append(out, difftui.File{
				Path:      f.Path,
				Settled:   f.ReadState == types.DiffReadRead,
				Generated: f.Generated(),
				Facts:     diffFileFacts(f),
			})
		}
	}
	return out
}

func diffOrderSteps(o *types.DiffOrder) int {
	n := 0
	for _, g := range o.Groups {
		n += len(g.Steps)
	}
	return n
}

// unreadReport is what `magus diff --unread` emits. State is "unknown" when the read marks
// could not be read, and Unread is then empty: an unreadable store says nothing about what is
// unread.
type unreadReport struct {
	Source string              `json:"source"`
	State  string              `json:"state"`
	Reason string              `json:"reason,omitempty"`
	Hunks  int                 `json:"hunks"`
	Unread []types.DiffHunkRef `json:"unread"`
}

const (
	unreadKnown   = "known"
	unreadUnknown = "unknown"
)

// buildUnreadReport sets the hunks of patch against the digests viewed holds. loadErr is the
// error from reading them, if any.
func buildUnreadReport(source, patch string, viewed []string, loadErr error) unreadReport {
	parsed := changeset.ParseHunks(patch)
	rep := unreadReport{Source: source, State: unreadKnown, Unread: []types.DiffHunkRef{}}
	for _, f := range parsed {
		rep.Hunks += len(f.Hunks)
	}
	if loadErr != nil {
		rep.State, rep.Reason = unreadUnknown, loadErr.Error()
		return rep
	}
	rep.Unread = append(rep.Unread, changeset.UnreadHunks(parsed, viewed)...)
	return rep
}

// printUnread implements `magus diff --unread`. It returns nil whatever the report says: it
// reports, and a read count that blocked a push would be a gate on the measure it reports.
func printUnread(m *magus.Magus, src diffInput, opts OutputOptions, patch string) error {
	if opts.Format != outputText && opts.Format != outputJSON {
		return usagef("magus diff: --unread prints text or -o json, not -o %v", opts.Format)
	}
	viewed, err := changeset.NewStore(m.CacheDir()).LoadViewed()
	rep := buildUnreadReport(src.label, patch, viewed, err)
	if opts.Format == outputJSON {
		return emitFormatted(opts, rep)
	}
	next := hint.Diff.String()
	if src.kind == inputRevRange {
		next = hint.Diff.With("--rev", src.base+"..."+src.head)
	}
	for _, line := range unreadLines(rep, patch, next) {
		fmt.Println(line)
	}
	return nil
}

// unreadLines renders the report for a person. The ranges come from patch, since a report
// names hunks by digest and a reader finds them by line. next is the command that opens the
// same changeset in the viewer.
func unreadLines(rep unreadReport, patch, next string) []string {
	if rep.State == unreadUnknown {
		return []string{
			fmt.Sprintf("read state unknown for %s: the read marks could not be read (%s)", rep.Source, rep.Reason),
			fmt.Sprintf("%d hunks in the range; none is called unread", rep.Hunks),
		}
	}
	if len(rep.Unread) == 0 {
		return []string{fmt.Sprintf("every hunk of %s is marked read (%d hunks)", rep.Source, rep.Hunks)}
	}
	ranges := map[string]string{}
	for _, f := range changeset.ParseHunks(patch) {
		for _, h := range f.Hunks {
			ranges[fmt.Sprintf("%s#%d", f.Path, h.Index)] = newRange(f.Path, h.NewStart, h.NewCount)
		}
	}
	lines := []string{fmt.Sprintf("%d of %d hunks in %s are not marked read", len(rep.Unread), rep.Hunks, rep.Source)}
	for _, r := range rep.Unread {
		at := ranges[fmt.Sprintf("%s#%d", r.Path, r.Index)]
		if at == "" {
			at = r.Path
		}
		lines = append(lines, "  "+at)
	}
	return append(lines, "mark them read in the viewer: "+next)
}
