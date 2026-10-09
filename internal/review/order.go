package review

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/types"
)

// OrderLines renders the reading order for a terminal: each group, each step with its hunks
// and the sentence that placed them, then the completeness line. A generated group is folded
// to one line unless showGenerated. Nothing for a diff with no order, because the note it
// carries says why.
func OrderLines(rev types.Diff, showGenerated bool) []string {
	order := rev.Order
	if order == nil {
		return nil
	}
	ranges := hunkRanges(rev)
	lines := []string{"", "reading order"}
	for gi, g := range order.Groups {
		lines = append(lines, "", fmt.Sprintf("group %d of %d: %s", gi+1, len(order.Groups), GroupTitle(g)))
		if g.Kind == types.DiffGroupGenerated && !showGenerated {
			lines = append(lines, "  folded: a target rewrites these; show them with --generated")
			continue
		}
		for _, s := range g.Steps {
			lines = append(lines, fmt.Sprintf("  step %d", s.Number))
			for _, h := range s.Hunks {
				at := ranges[h.Ref.Key()]
				if at == "" {
					at = h.Ref.Path
				}
				head := "    " + at
				if h.Label != "" {
					head += "  " + h.Label
				}
				lines = append(lines, head, "        "+h.Why.Text)
			}
		}
	}
	return append(lines, "", OrderCountLine(order.Count))
}

// GroupTitle is "kind around label (n hunks)", the heading a group carries wherever it is shown.
func GroupTitle(g types.DiffGroup) string {
	title := string(g.Kind)
	if g.Label != "" {
		title += " around " + g.Label
	}
	if g.HunkCount == 1 {
		return title + " (1 hunk)"
	}
	return fmt.Sprintf("%s (%d hunks)", title, g.HunkCount)
}

// OrderCountLine states what was placed, and names every hunk or file the order could not
// account for, so a reader never takes a short list for a whole one.
func OrderCountLine(c types.DiffOrderCount) string {
	line := fmt.Sprintf("%d hunks, %d placed", c.HunkCount, c.Placed)
	if len(c.Repeated) > 0 {
		line += fmt.Sprintf("; %d placed more than once: %s", len(c.Repeated), refList(c.Repeated))
	}
	if len(c.Missing) > 0 {
		line += fmt.Sprintf("; %d not placed: %s", len(c.Missing), refList(c.Missing))
	}
	if len(c.FilesWithoutHunks) > 0 {
		line += fmt.Sprintf("; %d files with no hunk to show: %s", len(c.FilesWithoutHunks), strings.Join(c.FilesWithoutHunks, ", "))
	}
	return line
}

func refList(refs []types.DiffHunkRef) string {
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		names = append(names, r.Key())
	}
	return strings.Join(names, ", ")
}

// hunkRanges names each hunk's lines of the new file, keyed by DiffHunkRef.Key.
func hunkRanges(rev types.Diff) map[string]string {
	out := map[string]string{}
	for _, f := range rev.Files {
		for _, h := range f.Hunks {
			out[types.DiffHunkRef{Path: f.Path, Index: h.Index}.Key()] = changeset.NewRange(f.Path, h.NewStart, h.NewCount)
		}
	}
	return out
}
