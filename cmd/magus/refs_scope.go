package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
)

// checkRefsInput refuses what a form would otherwise drop:
// an answer that ignored part of its question exits 0 and reads as complete.
// It runs after refsSourceWindow, so rf.Source counts what implies it.
func checkRefsInput(rf *flaggen.RefsFlags, fs *flag.FlagSet, pos []string) error {
	if rf.Text {
		for _, name := range []string{flaggen.FlagRefsOccurrences, flaggen.FlagRefsDefinition, flaggen.FlagRefsSource, flaggen.FlagRefsRename, flaggen.FlagRefsRefresh, flaggen.FlagRefsOffset} {
			if flagGiven(fs, name) {
				return exit.Usagef("magus refs: --text searches text with no symbol index, so --%s has nothing to act on; drop one of them", name)
			}
		}
		return nil
	}
	body := rf.Definition || rf.Source && !rf.Occurrences
	switch {
	case rf.Rename != "" && (rf.Occurrences || body):
		return exit.Usagef("magus refs: --rename takes none of --text, --occurrences, --definition, --source")
	case rf.Definition && rf.Occurrences:
		return exit.Usagef("magus refs: --definition lists where the body is and --occurrences every site; drop one (--occurrences --source prints each site's lines)")
	case flagGiven(fs, flaggen.FlagRefsLimit) && !rf.Occurrences:
		return exit.Usagef("magus refs: --limit cuts --occurrences sites or --text matches, and this list has neither; add --occurrences")
	case len(pos) > 1 && (rf.Rename != "" || body):
		return exit.Usagef("magus refs: --rename, --definition and --source take one symbol and no path (got %q); to scope a symbol's sites, use `%s`",
			pos[1], hint.Refs.With(shellArg(pos[0]), "--occurrences", shellArg(pos[1])))
	}
	return nil
}

// refsAgain respells the command that asked, with its paths, less drop.
func refsAgain(fs *flag.FlagSet, pos []string, drop ...string) string {
	again := commandAgain(hint.Refs.With, fs, pos[0], drop...)
	for _, p := range pos[1:] {
		again += " " + shellArg(p)
	}
	return again
}

// occurrencePage is the sites trailing paths, --limit and --offset keep.
// again is the command that asked, less the last two flags.
type occurrencePage struct {
	scopes        []string // workspace-relative, slash-separated
	limit, offset int
	again         string
}

// refsOccurrencePage validates the paging flags and the trailing paths,
// and spells the next command. A path that does not exist is an error,
// since a mistyped second symbol reaches here reading as one.
func refsOccurrencePage(rf *flaggen.RefsFlags, fs *flag.FlagSet, root string, pos []string) (occurrencePage, error) {
	if rf.Text {
		return occurrencePage{}, nil
	}
	if !rf.Occurrences && flagGiven(fs, flaggen.FlagRefsOffset) {
		return occurrencePage{}, exit.Usagef("magus refs: --offset narrows --occurrences sites; add --occurrences")
	}
	if rf.Occurrences && (rf.Limit < 0 || rf.Offset < 0) {
		return occurrencePage{}, exit.Usagef("magus refs: --limit and --offset take 0 or more sites")
	}
	abs, err := resolveSearchScopes(root, pos[1:])
	if err != nil {
		return occurrencePage{}, exit.Usagef("magus refs: a trailing word scopes the answer to a path: %v", err)
	}
	scopes := make([]string, 0, len(abs))
	for _, a := range abs {
		rel, err := filepath.Rel(root, a)
		if err != nil {
			return occurrencePage{}, exit.Usagef("magus refs: %v", err)
		}
		scopes = append(scopes, filepath.ToSlash(rel))
	}
	return occurrencePage{scopes: scopes, limit: rf.Limit, offset: rf.Offset, again: refsAgain(fs, pos, flaggen.FlagRefsLimit, flaggen.FlagRefsOffset)}, nil
}

// holds reports file at or under a trailing path. No path holds every file.
func (p occurrencePage) holds(file string) bool {
	return len(p.scopes) == 0 || slices.ContainsFunc(p.scopes, func(scope string) bool {
		return scope == "." || file == scope || strings.HasPrefix(file, scope+"/")
	})
}

// scoped keeps the files at or under the trailing paths.
func (p occurrencePage) scoped(files []types.SymbolOccurrenceFile) []types.SymbolOccurrenceFile {
	return slices.DeleteFunc(slices.Clone(files), func(f types.SymbolOccurrenceFile) bool { return !p.holds(f.File) })
}

// scopeNote counts what the paths left out, so a narrowed count reads as one.
func (p occurrencePage) scopeNote(files []types.SymbolOccurrenceFile, kept int) string {
	if len(p.scopes) == 0 {
		return ""
	}
	total := 0
	for _, f := range files {
		total += len(f.Occurrences)
	}
	return fmt.Sprintf("%s keeps %d of %d sites", strings.Join(p.scopes, ", "), kept, total)
}

// scopedRefs narrows the plain list to the trailing paths and recounts it,
// with a note saying what it left out.
func (p occurrencePage) scopedRefs(out types.KnowledgeRefsOutput) (types.KnowledgeRefsOutput, string) {
	if len(p.scopes) == 0 {
		return out, ""
	}
	total := out.RefCount
	out.Refs = slices.DeleteFunc(slices.Clone(out.Refs), func(r types.KnowledgeRefSite) bool { return !p.holds(r.File) })
	out.FileCount, out.RefCount = len(out.Refs), 0
	for _, r := range out.Refs {
		out.RefCount += r.Count
	}
	return out, fmt.Sprintf("%s keeps %d of %d references", strings.Join(p.scopes, ", "), out.RefCount, total)
}

// cut keeps the page's sites and drops a file left with none.
func (p occurrencePage) cut(files []types.SymbolOccurrenceFile) (kept []types.SymbolOccurrenceFile, shown int) {
	skip := p.offset
	for _, f := range files {
		sites := f.Occurrences[min(skip, len(f.Occurrences)):]
		skip -= len(f.Occurrences) - len(sites)
		if p.limit > 0 {
			sites = sites[:min(p.limit-shown, len(sites))]
		}
		if len(sites) > 0 {
			f.Occurrences = sites
			kept = append(kept, f)
			shown += len(sites)
		}
	}
	return kept, shown
}

// note says what the page cut, and is empty when it cut nothing.
func (p occurrencePage) note(shown, total int) string {
	switch {
	case shown == total:
		return ""
	case p.offset >= total:
		return fmt.Sprintf("0 of %d sites; --offset %d is past the last site", total, p.offset)
	case p.offset+shown < total:
		return fmt.Sprintf("%d of %d sites; next: %s --limit %d --offset %d", shown, total, p.again, p.limit, p.offset+shown)
	}
	return fmt.Sprintf("%d of %d sites", shown, total)
}
