package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// symbolDelta is what a diff did to the symbols around it, beside what any goal asked of
// them. Added are the exported Go symbols it introduced, qualified `Type#Member` for a
// member; Left are the symbols that lost their last use in it and are still defined, each
// with where it is defined. Both sorted.
type symbolDelta struct {
	Added, Left []string
}

// readSymbolDelta reads the delta of the diff from base to the tree at root over changed,
// against g, the graph of that same tree. false means it could not be read: no graph, no
// version control, no base, or regions the backend would not report.
//
// Added is each symbol a new-side line of a changed non-test Go file lands in whose name
// the base copy of that file does not hold. Left is each unreferenced symbol that is not a
// method, a field or test code, whose name some changed file held at base and none holds
// now. Both read names as identifiers in text, so a name that survives only in a comment
// still counts as held: they under-report rather than invent.
func readSymbolDelta(ctx context.Context, root string, g *knowledge.Graph, driver types.VCSDriver, base string, changed []string) (symbolDelta, bool) {
	if g == nil || driver == nil || base == "" {
		return symbolDelta{}, false
	}
	if len(changed) == 0 {
		return symbolDelta{}, true
	}
	before, after := map[string]string{}, map[string]string{}
	for _, p := range changed {
		// A path missing on either side reads as empty: an added file held nothing at base,
		// and a deleted one holds nothing now.
		before[p], _ = driver.ReadFileAt(ctx, root, base, p)
		if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p))); err == nil {
			after[p] = string(b)
		}
	}

	var delta symbolDelta
	var goFiles []types.FileChange
	for _, p := range changed {
		if _, present := after[p]; present && strings.HasSuffix(p, ".go") && !types.IsTestPath(p) {
			goFiles = append(goFiles, types.FileChange{Path: p})
		}
	}
	if len(goFiles) > 0 {
		// A graph that places no symbol in any changed Go file was not indexed over them,
		// and reading it would report every addition as none.
		if !slices.ContainsFunc(goFiles, func(f types.FileChange) bool { return g.SymbolAt(f.Path, math.MaxInt).ID != "" }) {
			return symbolDelta{}, false
		}
		regions, err := driver.Regions(ctx, root, base, goFiles)
		if err != nil {
			return symbolDelta{}, false
		}
		seen := map[string]bool{}
		for _, r := range regions {
			if r.Side != types.RegionNew {
				continue
			}
			for line := r.Lines[0]; line <= r.Lines[1]; line++ {
				span := g.SymbolAt(r.File.Path, line)
				if span.ID == "" || seen[span.ID] {
					continue
				}
				seen[span.ID] = true
				name, exported := goExport(span.ID)
				if exported && knowledge.IndexIdentifier(before[r.File.Path], span.Label) < 0 {
					delta.Added = append(delta.Added, name)
				}
			}
		}
	}

	held := func(texts map[string]string, label string) bool {
		for _, p := range changed {
			if knowledge.IndexIdentifier(texts[p], label) >= 0 {
				return true
			}
		}
		return false
	}
	for _, e := range g.Unreferenced() {
		if types.IsTestPath(e.Source) || len(descriptorNames(e.ID)) != 1 {
			continue
		}
		if held(before, e.Label) && !held(after, e.Label) {
			delta.Left = append(delta.Left, e.Label+" ("+e.Source+")")
		}
	}
	slices.Sort(delta.Added)
	delta.Added = slices.Compact(delta.Added)
	slices.Sort(delta.Left)
	return delta, true
}

// goExport is a Go symbol's name as a goal spells it, `Type#Member` for a member, and
// whether code outside its package can name it: every step of the path exported.
func goExport(id string) (string, bool) {
	names := descriptorNames(id)
	if len(names) == 0 {
		return "", false
	}
	for _, n := range names {
		if r, _ := utf8.DecodeRuneInString(n); !unicode.IsUpper(r) {
			return "", false
		}
	}
	return strings.Join(names, "#"), true
}

// descriptorNames are the names along a symbol node id's path inside its package:
// `Goal#Signature.` is [Goal Signature], `Resolve().` is [Resolve], and a parameter adds its
// own name. The id is `symbol:<manager> <package> <descriptors>`, and the descriptors' own
// namespace ends at their last `/`.
func descriptorNames(id string) []string {
	parts := strings.SplitN(strings.TrimPrefix(id, types.KindSymbol+":"), " ", 3)
	path := parts[len(parts)-1]
	if i := strings.LastIndex(path, "/"); i >= 0 {
		path = path[i+1:]
	}
	return strings.FieldsFunc(strings.ReplaceAll(path, "`", ""), func(r rune) bool {
		return strings.ContainsRune("#.():[]!", r) || unicode.IsSpace(r)
	})
}
