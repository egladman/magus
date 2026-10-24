package review

import (
	"cmp"
	"fmt"
	"slices"
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
	threads := map[string][]types.DiffThreadRef{}
	for _, f := range rev.Files {
		for _, t := range f.Threads {
			if t.Hunk >= 0 {
				key := types.DiffHunkRef{Path: f.Path, Index: t.Hunk}.Key()
				threads[key] = append(threads[key], t)
			}
		}
	}
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
				for _, t := range threads[h.Ref.Key()] {
					lines = append(lines, "        "+ThreadRefLine(t))
				}
			}
			for _, sig := range stepSignatures(rev, s) {
				lines = append(lines, "    signature: "+sig)
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

// stepSignatures is one line per symbol changed in the step's hunks, sorted by id: the
// declaration as the indexer rendered it, then "was" and the base declaration when the
// signature changed. A symbol the indexer rendered no declaration for says so under its label,
// so a missing line never reads as an unchanged one.
func stepSignatures(rev types.Diff, step types.DiffStep) []string {
	type changed struct {
		path string
		sym  types.DiffSymbol
	}
	var found []changed
	seen := map[string]bool{}
	for _, sh := range step.Hunks {
		for _, f := range rev.Files {
			if f.Path != sh.Ref.Path {
				continue
			}
			for _, h := range f.Hunks {
				if h.Index != sh.Ref.Index {
					continue
				}
				for _, id := range h.Symbols {
					key := fmt.Sprintf("%s\x00%s", f.Path, id)
					if seen[key] {
						continue
					}
					seen[key] = true
					for _, sym := range f.Symbols {
						if sym.ID == id {
							found = append(found, changed{f.Path, sym})
						}
					}
				}
			}
		}
	}
	slices.SortStableFunc(found, func(a, b changed) int {
		return cmp.Or(cmp.Compare(a.sym.ID, b.sym.ID), cmp.Compare(a.path, b.path))
	})
	lines := make([]string, 0, len(found))
	for _, c := range found {
		sig := strings.Join(strings.Fields(c.sym.Signature), " ")
		if sig == "" {
			lines = append(lines, cmp.Or(c.sym.Label, c.sym.Qualified, c.sym.ID)+" (no signature from the indexer)")
			continue
		}
		base := strings.Join(strings.Fields(c.sym.BaseSignature), " ")
		if base != "" && base != sig {
			sig += " was " + base
		}
		lines = append(lines, sig)
	}
	return lines
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
