package magus

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// orderOccurrenceWorkers bounds the occurrence reads in flight: each one decodes the declared
// symbol indexes, so an unbounded fan-out over a large changeset would hold them all at once.
const orderOccurrenceWorkers = 4

// attachOrder sets out.Order from the hunks attachHunks filled. indexCurrent says the symbol
// index describes the tree under review; without that the sites and implementations it would
// supply are unknown, and an order built on their absence would read as "nothing is related".
// Order stays nil then, and a Note names the rebuild.
func (m *Magus) attachOrder(ctx context.Context, out *types.Diff, graph *knowledge.Graph, patch string, indexCurrent bool) {
	if !indexCurrent {
		out.Notes = append(out.Notes, "reading order skipped: the symbol index is not current for this tree; rebuild it with `"+hint.GraphBuild.String()+"`")
		return
	}
	sites, note := m.orderSites(ctx, out.Files)
	if note != "" {
		out.Notes = append(out.Notes, note)
	}
	order := buildOrder(m.ws.Root, patch, out.Files, graph, sites)
	out.Order = &order
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
func workingTreeHolds(root string, f changeset.File) bool {
	var lines []string
	read := false
	for _, h := range f.Hunks {
		for _, r := range h.Rows {
			if r.NewLine == nil {
				continue
			}
			if !read {
				b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
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
// file. KnowledgeSymbol.Refs is capped per file and cannot say which line, so each symbol goes
// back to the index through SymbolOccurrences. The note is non-empty when the answer is
// partial: an index magus could not decode drops that project's sites from the list.
func (m *Magus) orderSites(ctx context.Context, files []types.DiffFile) ([]changeset.OrderSite, string) {
	changed := map[string]bool{}
	for _, f := range files {
		changed[f.Path] = true
	}
	ids := changedDefinitions(files)
	reads := make([]SymbolOccurrenceRead, len(ids))
	answered := make([]bool, len(ids))

	var g errgroup.Group
	g.SetLimit(orderOccurrenceWorkers)
	for i, id := range ids {
		g.Go(func() error {
			reads[i], answered[i] = m.SymbolOccurrences(ctx, strings.TrimPrefix(id, types.KindSymbol+":"))
			return nil
		})
	}
	_ = g.Wait()

	var sites []changeset.OrderSite
	unreadable := map[string]bool{}
	unanswered := false
	for i, id := range ids {
		if !answered[i] {
			unanswered = true
			continue
		}
		for _, gap := range reads[i].Unreadable {
			unreadable[gap.Project.Name] = true
		}
		for _, f := range reads[i].Files {
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

	switch {
	case unanswered:
		return sites, "reading order is partial: the symbol indexes could not be listed, so some uses of the changed symbols are missing"
	case len(unreadable) > 0:
		names := make([]string, 0, len(unreadable))
		for p := range unreadable {
			names = append(names, p)
		}
		slices.Sort(names)
		return sites, "reading order is partial: the symbol index of " + strings.Join(names, ", ") + " could not be read; rebuild it with `" + hint.GraphBuild.String() + "`"
	}
	return sites, ""
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
