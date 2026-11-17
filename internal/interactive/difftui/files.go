package difftui

import (
	"fmt"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/types/diff"
)

// OrderedFiles projects the reading order onto the viewer: one file per step and path, in
// step order, each hunk's Why sentence as a fact. Every hunk lands in exactly one file, so the
// receipts earnedSync mints per path still add up. A diff with no order, or one whose count
// says it did not place every hunk, keeps today's file-by-file projection.
func OrderedFiles(rev diff.Diff, parsed []changeset.FileHunks) []File {
	if rev.Order == nil || !rev.Order.Count.Complete {
		return joinFiles(rev, parsed)
	}
	byKey := map[string]changeset.Hunk{}
	for _, f := range parsed {
		for _, h := range f.Hunks {
			byKey[diff.HunkRef{Path: f.Path, Index: h.Index}.Key()] = h
		}
	}
	files := map[string]diff.File{}
	for _, f := range rev.Files {
		files[f.Path] = f
	}

	var out []File
	shown := map[string]bool{}
	steps := review.OrderSteps(rev.Order)
	for _, g := range rev.Order.Groups {
		for _, s := range g.Steps {
			current := map[string]int{}
			for _, sh := range s.Hunks {
				h, ok := byKey[sh.Ref.Key()]
				if !ok {
					return joinFiles(rev, parsed)
				}
				at, seen := current[sh.Ref.Path]
				if !seen {
					src := files[sh.Ref.Path]
					piece := File{
						Path:      sh.Ref.Path,
						Settled:   src.ReadState == diff.ReadRead,
						Generated: g.Kind == diff.GroupGenerated || src.Generated(),
						Facts:     []string{fmt.Sprintf("step %d of %d, %s", s.Number, steps, review.GroupTitle(g))},
					}
					if !shown[sh.Ref.Path] {
						piece.Facts = append(piece.Facts, review.FileFacts(src)...)
						shown[sh.Ref.Path] = true
					}
					out = append(out, piece)
					at = len(out) - 1
					current[sh.Ref.Path] = at
				}
				fact := sh.Why.Text
				if sh.Label != "" {
					fact = fmt.Sprintf("%s: %s", sh.Label, fact)
				}
				out[at].Facts = append(out[at].Facts, fact)
				out[at].Hunks = append(out[at].Hunks, Hunk{
					Index: h.Index, Header: h.Header, Lines: displayOr(h), Digest: h.Digest,
					NewStart: h.NewStart, Declaration: h.Declaration,
					Emph: changeset.RawLineEmphasis(h),
				})
			}
		}
	}
	// A file with no hunk (a rename, a mode change, a binary) has no step; it follows the order
	// so the viewer still lists it.
	for _, f := range rev.Files {
		if !shown[f.Path] {
			out = append(out, File{
				Path:      f.Path,
				Settled:   f.ReadState == diff.ReadRead,
				Generated: f.Generated(),
				Facts:     review.FileFacts(f),
			})
		}
	}
	return out
}

// joinFiles joins the annotations to the patch: one is ordered by consequence, the other
// by whatever the VCS emitted, and the reader wants the first order with the second's text.
//
// The annotation order is authoritative and is never recomputed here: types.Diff.
// SortForReading is the single definition of review order.
func joinFiles(rev diff.Diff, parsed []changeset.FileHunks) []File {
	byPath := make(map[string][]changeset.Hunk, len(parsed))
	for _, f := range parsed {
		byPath[f.Path] = f.Hunks
	}
	out := make([]File, 0, len(rev.Files))
	for _, f := range rev.Files {
		// Settled is READ-AND-UNMOVED, never merely read: DiffReadStale means a receipt exists at
		// DIFFERENT content, which is the file that most needs a second look rather than the least.
		file := File{
			Path:      f.Path,
			Settled:   f.ReadState == diff.ReadRead,
			Generated: f.Generated(),
			Facts:     review.FileFacts(f),
		}
		for _, h := range byPath[f.Path] {
			file.Hunks = append(file.Hunks, Hunk{
				// Display when the parser found something a renderer would obey, Lines
				// otherwise. The viewer never sees the raw form of a deceptive line.
				Index: h.Index, Header: h.Header, Lines: displayOr(h), Digest: h.Digest,
				NewStart: h.NewStart, Declaration: h.Declaration,
				Emph: changeset.RawLineEmphasis(h),
			})
		}
		out = append(out, file)
	}
	return out
}

// displayOr is a hunk's render-safe lines, falling back to its real ones.
//
// The fallback is the common case: displayLines returns nil unless a line carried a character a
// renderer obeys but a reader cannot see, so an honest patch reaches the viewer untouched.
func displayOr(h changeset.Hunk) []string {
	if h.Display != nil {
		return h.Display
	}
	return h.Lines
}
