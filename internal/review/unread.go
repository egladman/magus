package review

import (
	"fmt"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/types"
)

// FilterUnread narrows rev to the hunks no read mark covers: a file keeps only its unread hunks
// and is dropped when none is left, and the reading order keeps its steps' unread hunks under
// their original step numbers. viewed holds the digests of the hunks marked read, and loadErr is
// the error from reading them.
//
// When loadErr is set the result holds no file at all and Unread says the read state is unknown:
// an unreadable store says nothing about what is unread, so no hunk is called unread.
func FilterUnread(rev types.Diff, viewed []string, loadErr error) types.Diff {
	total := 0
	for _, f := range rev.Files {
		total += len(f.Hunks)
	}
	if loadErr != nil {
		rev.Files, rev.Order = nil, nil
		rev.Unread = &types.DiffUnread{ReadState: types.DiffReadStateUnknown, Reason: loadErr.Error(), Hunks: total}
		return rev
	}
	read := make(map[string]bool, len(viewed))
	for _, d := range viewed {
		read[d] = true
	}
	keep := map[string]bool{}
	files := make([]types.DiffFile, 0, len(rev.Files))
	for _, f := range rev.Files {
		var hunks []types.DiffHunk
		for _, h := range f.Hunks {
			if !read[h.Digest] {
				hunks = append(hunks, h)
				keep[types.DiffHunkRef{Path: f.Path, Index: h.Index}.Key()] = true
			}
		}
		if len(hunks) == 0 {
			continue
		}
		f.Hunks = hunks
		files = append(files, f)
	}
	rev.Files = files
	if rev.Order != nil {
		rev.Order = filterOrder(*rev.Order, keep)
	}
	rev.Unread = &types.DiffUnread{ReadState: types.DiffReadStateKnown, Hunks: total, Unread: len(keep)}
	return rev
}

// filterOrder keeps the placements keep names. Step numbers are not reassigned, because each
// Why names the step it was placed against by number.
func filterOrder(o types.DiffOrder, keep map[string]bool) *types.DiffOrder {
	out := types.DiffOrder{}
	for _, g := range o.Groups {
		var steps []types.DiffStep
		n := 0
		for _, s := range g.Steps {
			var hunks []types.DiffStepHunk
			for _, h := range s.Hunks {
				if keep[h.Ref.Key()] {
					hunks = append(hunks, h)
				}
			}
			if len(hunks) > 0 {
				s.Hunks = hunks
				steps = append(steps, s)
				n += len(hunks)
			}
		}
		if len(steps) > 0 {
			g.Steps, g.HunkCount = steps, n
			out.Groups = append(out.Groups, g)
			out.Count.Placed += n
		}
	}
	out.Count.HunkCount = len(keep)
	out.Count.Repeated = keptRefs(o.Count.Repeated, keep)
	out.Count.Missing = keptRefs(o.Count.Missing, keep)
	out.Count.Complete = out.Count.Placed == out.Count.HunkCount && len(out.Count.Repeated) == 0 && len(out.Count.Missing) == 0
	return &out
}

func keptRefs(refs []types.DiffHunkRef, keep map[string]bool) []types.DiffHunkRef {
	var out []types.DiffHunkRef
	for _, r := range refs {
		if keep[r.Key()] {
			out = append(out, r)
		}
	}
	return out
}

// HunkNames is one path:start-end per hunk of rev, in file order: the -o name shape of a
// report narrowed to hunks.
func HunkNames(rev types.Diff) []string {
	var out []string
	for _, f := range rev.Files {
		for _, h := range f.Hunks {
			out = append(out, changeset.NewRange(f.Path, h.NewStart, h.NewCount))
		}
	}
	return out
}

// UnreadLine is the one line that states what the unread filter found, for a person.
func UnreadLine(u types.DiffUnread, source string) string {
	switch {
	case u.ReadState == types.DiffReadStateUnknown:
		return "read state unknown for " + source + "; no hunk is called unread"
	case u.Unread == 0:
		return fmt.Sprintf("every hunk of %s is marked read (%d %s)", source, u.Hunks, plural(u.Hunks, "hunk", "hunks"))
	}
	return fmt.Sprintf("%d of %d hunks in %s are not marked read", u.Unread, u.Hunks, source)
}
