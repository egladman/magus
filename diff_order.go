package magus

import (
	"cmp"
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// hunkPlace is where a touched symbol's changed lines fall within its file's patch.
type hunkPlace struct {
	// hunks are the indexes of the hunks that changed a line of the symbol, ascending.
	hunks []int
	// start and extent are the first line and the line count of its definition, which order
	// symbols a hunk shares: the smaller extent is the innermost. Zero where the symbol has
	// no recorded range.
	start, extent int
}

// attachHunks fills each file's Hunks from the patch: every hunk, with its parsed address,
// and the listed symbols whose changed lines it holds, innermost first (see DiffHunk.Symbols,
// which changeset.OrderHunks reads the first of to label a hunk). placesByFile says which hunks
// changed each file's symbols; a listed symbol absent from it sits in no hunk.
func attachHunks(files []types.DiffFile, patch string, placesByFile map[string]map[string]hunkPlace) {
	parsed := map[string][]changeset.Hunk{}
	for _, pf := range changeset.Parse(patch) {
		parsed[pf.Path] = pf.Hunks
	}
	for i := range files {
		f := &files[i]
		for _, h := range parsed[f.Path] {
			dh := types.DiffHunk{
				Index: h.Index, Digest: h.Digest,
				OldStart: h.OldStart, OldCount: h.OldCount, NewStart: h.NewStart, NewCount: h.NewCount,
				Declaration: h.Declaration,
			}
			var ids []string
			held := map[string]hunkPlace{}
			for _, s := range f.Symbols {
				if p := placesByFile[f.Path][s.ID]; slices.Contains(p.hunks, h.Index) {
					ids = append(ids, s.ID)
					held[s.ID] = p
				}
			}
			slices.SortStableFunc(ids, func(a, b string) int {
				return cmp.Or(cmp.Compare(held[a].extent, held[b].extent), cmp.Compare(held[b].start, held[a].start))
			})
			dh.Symbols = ids
			f.Hunks = append(f.Hunks, dh)
		}
	}
}

// attachOrder sets out.Order from the hunks attachHunks filled. skip, when not empty, says why
// the symbol index cannot back an order for the tree under review (see orderSkip): the sites
// and implementations it would supply are unknown, and an order built on their absence would
// read as "nothing is related". Order stays nil then, and a Note names the cause.
func (m *Magus) attachOrder(ctx context.Context, out *types.Diff, graph *knowledge.Graph, patch, skip string) {
	if skip != "" {
		out.Notes = append(out.Notes, "reading order skipped: "+skip+"; rebuild it with `"+hint.GraphBuild.String()+"`")
		return
	}
	sites, note := m.orderSites(ctx, out.Files)
	if note != "" {
		out.Notes = append(out.Notes, note)
	}
	order := buildOrder(m.ws.Root, patch, out.Files, graph, sites)
	out.Order = &order
}

// orderSkip says why the symbol index cannot back a reading order, or "" when it can. touched
// are the projects holding a changed file and indexed says the graph loaded symbols at all.
//
// A touched symbol-capable project with no index built is a reason of its own, even when
// another project is indexed: the uses of a changed symbol that live there are unknown, so
// "stands alone" would be a claim about code nobody read.
func (m *Magus) orderSkip(ctx context.Context, touched []string, freshErr error, indexed bool) string {
	capable := m.symbolCapableIn(touched)
	var built map[string]time.Time
	listed := true
	if freshErr == nil && indexed && len(capable) > 0 {
		built, listed = SymbolIndexTimes(ctx, m, m.Root(), m.cfg)
	}
	return indexGap(freshErr, indexed, capable, built, listed, func(path string) string {
		return types.NewProjectRef(path, filepath.Join(m.ws.Root, filepath.FromSlash(path))).Display()
	})
}

// indexGap is the decision orderSkip makes, over what it gathered. built maps a project path to
// when its oldest index was written, and listed is false when that could not be read.
func indexGap(freshErr error, indexed bool, capable []string, built map[string]time.Time, listed bool, display func(path string) string) string {
	switch {
	case freshErr != nil:
		return "the symbol index is not current for this tree"
	case len(capable) == 0:
		return ""
	case !indexed:
		return "the symbol index is not current for this tree"
	case !listed:
		return "the symbol indexes could not be listed"
	}
	var missing []string
	for _, p := range capable {
		if _, ok := built[p]; !ok {
			missing = append(missing, display(p))
		}
	}
	if len(missing) == 0 {
		return ""
	}
	slices.Sort(missing)
	return "no symbol index is built for " + strings.Join(missing, ", ")
}

// buildOrder orders the hunks of files. root is where the working tree lives, which decides
// the files whose hunks the index cannot place; sites are the uses of the changed symbols.
func buildOrder(root, patch string, files []types.DiffFile, graph *knowledge.Graph, sites []changeset.OrderSite) types.DiffOrder {
	return changeset.OrderHunks(changeset.OrderInput{
		Files:      orderFiles(files, movedFiles(root, patch)),
		Sites:      sites,
		Implements: orderLinks(graph, files),
		IsTest:     knowledge.IsTestPath,
	})
}

// orderFiles maps the reviewed files onto the ordering input.
func orderFiles(files []types.DiffFile, moved map[string]bool) []changeset.OrderFile {
	out := make([]changeset.OrderFile, 0, len(files))
	for _, f := range files {
		out = append(out, changeset.OrderFile{
			Path:      f.Path,
			Generated: f.Generated(),
			Moved:     moved[f.Path],
			Hunks:     f.Hunks,
			Symbols:   f.Symbols,
			Reach:     f.ReachOr(0),
		})
	}
	return out
}

// movedFiles names the files whose working tree does not hold the lines the patch puts there.
// The symbol index reads the working tree, so its line numbers describe such a file's hunks
// only by accident: a range review of a head that is not checked out lands here, and so does
// a patch handed over from elsewhere.
func movedFiles(root, patch string) map[string]bool {
	moved := map[string]bool{}
	for _, f := range changeset.Parse(patch) {
		if f.Binary || len(f.Hunks) == 0 {
			continue
		}
		if !workingTreeHolds(root, f) {
			moved[f.Path] = true
		}
	}
	return moved
}

// workingTreeHolds reports whether every context and added row of f sits at its line of the
// working file. A file with no such row, a pure deletion, has nothing to compare and holds.
// A path that leaves root holds nothing: a patch handed over from elsewhere may name
// `../../x`, and magus does not read outside the workspace for it.
func workingTreeHolds(root string, f changeset.File) bool {
	var lines []string
	read := false
	for _, h := range f.Hunks {
		for _, r := range h.Rows {
			if r.NewLine == nil {
				continue
			}
			if !read {
				rel := filepath.FromSlash(f.Path)
				if !filepath.IsLocal(rel) {
					return false
				}
				b, err := os.ReadFile(filepath.Join(root, rel))
				if err != nil {
					return false
				}
				lines, read = strings.Split(string(b), "\n"), true
			}
			at := *r.NewLine - 1
			if at < 0 || at >= len(lines) || trimLine(lines[at]) != trimLine(r.Text) {
				return false
			}
		}
	}
	return true
}

func trimLine(s string) string { return strings.TrimRight(s, " \t\r") }

// changedDefinitions are the IDs of the symbols some hunk holds, ascending.
func changedDefinitions(files []types.DiffFile) []string {
	seen := map[string]bool{}
	var ids []string
	for _, f := range files {
		for _, h := range f.Hunks {
			for _, s := range h.Symbols {
				if !seen[s] {
					seen[s] = true
					ids = append(ids, s)
				}
			}
		}
	}
	slices.Sort(ids)
	return ids
}

// orderSites reads every verified use of the changed definitions that falls in a changed
// file. KnowledgeSymbol.Refs is capped per file and cannot say which line, so the symbols go
// back to the indexes through one SymbolsOccurrences pass. The note is non-empty when the
// answer is partial, and names every cause: an index magus could not decode drops that
// project's sites, and a cancelled or failed listing drops them all.
func (m *Magus) orderSites(ctx context.Context, files []types.DiffFile) ([]changeset.OrderSite, string) {
	changed := map[string]bool{}
	for _, f := range files {
		changed[f.Path] = true
	}
	ids := changedDefinitions(files)
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = strings.TrimPrefix(id, types.KindSymbol+":")
	}
	reads, listed := m.SymbolsOccurrences(ctx, keys)

	var sites []changeset.OrderSite
	unreadable := map[string]bool{}
	for i, id := range ids {
		read := reads[keys[i]]
		for _, gap := range read.Unreadable {
			unreadable[gap.Project.Name] = true
		}
		for _, f := range read.Files {
			if !changed[f.File] {
				continue
			}
			for _, o := range f.Occurrences {
				if o.Definition || o.Status != types.SymbolOccurrenceVerified {
					continue
				}
				sites = append(sites, changeset.OrderSite{Symbol: id, Path: f.File, Line: o.Line})
			}
		}
	}

	return sites, partialOrderNote(ctx.Err() != nil, listed, slices.Sorted(maps.Keys(unreadable)))
}

// partialOrderNote words why the use sites behind a reading order are incomplete, or returns ""
// when they are not. A cancelled context explains every gap the reads recorded, so it stands
// alone; otherwise each cause that applies is named.
func partialOrderNote(cancelled, listed bool, unreadable []string) string {
	var causes []string
	if cancelled {
		causes = append(causes, "it was cancelled before every symbol index was read, so some uses of the changed symbols are missing")
	} else {
		if !listed {
			causes = append(causes, "the symbol indexes could not be listed, so some uses of the changed symbols are missing")
		}
		if len(unreadable) > 0 {
			causes = append(causes, "the symbol index of "+strings.Join(unreadable, ", ")+" could not be read; rebuild it with `"+hint.GraphBuild.String()+"`")
		}
	}
	if len(causes) == 0 {
		return ""
	}
	return "reading order is partial: " + strings.Join(causes, "; and ")
}

// orderLinks pairs the changed symbols with the changed interfaces they implement, from the
// graph's implements edges.
func orderLinks(graph *knowledge.Graph, files []types.DiffFile) []changeset.OrderLink {
	if graph == nil {
		return nil
	}
	changed := map[string]bool{}
	for _, id := range changedDefinitions(files) {
		changed[id] = true
	}
	var links []changeset.OrderLink
	for _, e := range graph.Edges() {
		if e.Relation == types.RelationImplements && changed[e.Source] && changed[e.Target] {
			links = append(links, changeset.OrderLink{Implementer: e.Source, Interface: e.Target})
		}
	}
	return links
}
