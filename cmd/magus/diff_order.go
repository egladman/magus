package main

import (
	"fmt"
	"io"
	"os"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive/difftui"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/types"
)

// printDiffOrder writes the reading order under the file summary.
func printDiffOrder(w io.Writer, rev types.Diff, showGenerated bool) {
	for _, line := range review.OrderLines(rev, showGenerated) {
		fmt.Fprintln(w, line)
	}
}

// diffOrderTUIFiles projects the reading order onto the viewer: one file per step and path, in
// step order, each hunk's Why sentence as a fact. Every hunk lands in exactly one file, so the
// receipts earnedSync mints per path still add up. A diff with no order, or one whose count
// says it did not place every hunk, keeps today's file-by-file projection.
func diffOrderTUIFiles(rev types.Diff, parsed []changeset.FileHunks) []difftui.File {
	if rev.Order == nil || !rev.Order.Count.Complete {
		return diffTUIFiles(rev, parsed)
	}
	byKey := map[string]changeset.Hunk{}
	for _, f := range parsed {
		for _, h := range f.Hunks {
			byKey[types.DiffHunkRef{Path: f.Path, Index: h.Index}.Key()] = h
		}
	}
	files := map[string]types.DiffFile{}
	for _, f := range rev.Files {
		files[f.Path] = f
	}

	var out []difftui.File
	shown := map[string]bool{}
	steps := orderSteps(rev.Order)
	for _, g := range rev.Order.Groups {
		for _, s := range g.Steps {
			current := map[string]int{}
			for _, sh := range s.Hunks {
				h, ok := byKey[sh.Ref.Key()]
				if !ok {
					return diffTUIFiles(rev, parsed)
				}
				at, seen := current[sh.Ref.Path]
				if !seen {
					src := files[sh.Ref.Path]
					piece := difftui.File{
						Path:      sh.Ref.Path,
						Settled:   src.ReadState == types.DiffReadRead,
						Generated: g.Kind == types.DiffGroupGenerated || src.Generated(),
						Facts:     []string{fmt.Sprintf("step %d of %d, %s", s.Number, steps, review.GroupTitle(g))},
					}
					if !shown[sh.Ref.Path] {
						piece.Facts = append(piece.Facts, diffFileFacts(src)...)
						shown[sh.Ref.Path] = true
					}
					out = append(out, piece)
					at = len(out) - 1
					current[sh.Ref.Path] = at
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

func orderSteps(o *types.DiffOrder) int {
	n := 0
	for _, g := range o.Groups {
		n += len(g.Steps)
	}
	return n
}

// printUnread implements `magus diff --unread`. It returns nil whatever the report says: it
// reports, and a read count that blocked a push would be a gate on the measure it reports.
func printUnread(m *magus.Magus, src diffInput, opts OutputOptions, patch string) error {
	if opts.Format != outputText && opts.Format != outputJSON {
		return usagef("magus diff: --unread prints text or -o json, not -o %v", opts.Format)
	}
	viewed, err := changeset.NewStore(m.CacheDir()).LoadViewed()
	rep := changeset.BuildUnreadReport(src.label, patch, viewed, err)
	if opts.Format == outputJSON {
		return emitFormatted(opts, rep)
	}
	next := hint.Diff.String()
	if src.kind == inputRevRange {
		next = hint.Diff.With("--rev", src.base+"..."+src.head)
	}
	return changeset.WriteUnread(os.Stdout, rep, patch, next)
}
