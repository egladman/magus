package job

import (
	"cmp"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// findingsLimit is how many entries a text rendering of a findings block lists before it
// counts the rest.
const findingsLimit = 12

const deletionHeading = "deletion candidates in the write paths: nothing else names them"

// UnreferencedInPaths is the symbols nothing references whose source file a write path of
// row covers, sorted by id. A claim (`run.go#Execute`) covers its whole file here, the way
// the guard reads it. Tests, methods and fields are left out: a test is never called, a
// method may satisfy an interface the graph cannot see, and a field is read by reflection
// as often as by name. A read-only row writes nothing and gets nil.
//
// Candidates are findings for the holder to finish or delete, never goals: the graph
// measures references, it does not judge them.
func UnreferencedInPaths(row types.Job, unreferenced []types.UnreferencedEntry) []types.UnreferencedEntry {
	if row.ReadOnly || len(row.WritePaths) == 0 {
		return nil
	}
	var out []types.UnreferencedEntry
	for _, e := range unreferenced {
		if e.Kind == "Method" || e.Kind == "Field" || types.IsTestPath(e.Source) {
			continue
		}
		file, _, _ := strings.Cut(e.Source, ":")
		if slices.ContainsFunc(row.WritePaths, func(w string) bool { return covers(w, file) }) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b types.UnreferencedEntry) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// RenderDeletionCandidates writes candidates as the block [Terms] renders, for a command
// that prints no terms. Nothing is written for none.
func RenderDeletionCandidates(w io.Writer, candidates []types.UnreferencedEntry) {
	renderBlock(w, deletionHeading, deletionLines(candidates))
}

func renderBlock(w io.Writer, heading string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, heading)
	for _, l := range lines {
		fmt.Fprintf(w, "  %s\n", l)
	}
}

// deletionLines is one line per candidate up to findingsLimit, then the caveat
// the measurement carries. The graph cannot see reflection, interface dispatch or a
// consumer outside the workspace, so a candidate is a place to look before deleting.
func deletionLines(candidates []types.UnreferencedEntry) []string {
	if len(candidates) == 0 {
		return nil
	}
	shown := candidates[:min(len(candidates), findingsLimit)]
	lines := make([]string, 0, len(shown)+2)
	for _, c := range shown {
		line := c.Label
		if c.Kind != "" {
			line += " (" + strings.ToLower(c.Kind) + ")"
		}
		if c.Source != "" {
			line += " at " + c.Source
		}
		lines = append(lines, line)
	}
	if rest := len(candidates) - len(shown); rest > 0 {
		lines = append(lines, fmt.Sprintf("and %d more; -o json lists every one", rest))
	}
	return append(lines,
		"finish each or delete it; reflection, interface dispatch and consumers outside this workspace are invisible to this list, so confirm before deleting")
}

const referencedHeading = "referenced outside the write paths: files that use what the write paths define"

// ReferencedFile is one file outside a job's write paths that references a symbol a write
// path defines.
type ReferencedFile struct {
	// File is the write path to add when the job's change reaches it.
	File  string `json:"file"  yaml:"file"`
	Count int    `json:"count" yaml:"count"`
	// Uses are the write paths, or the files under a glob or directory write path, whose
	// symbols File references, in declaration order.
	Uses []string `json:"uses" yaml:"uses"`
}

// ReferencedOutside is every file outside row's write paths that references a symbol the
// write paths define: the tests and callers one hop out. referrers answers one file,
// narrowed to the declaration a claim (`run.go#Execute`) names; a glob or directory is
// expanded under root. A file a target in projects outputs is left out, since MGS3044
// reports it. Tests sort first, then by count. A read-only row gets nil.
//
// A finding, never a refusal: whether the change reaches a caller is the holder's call.
func ReferencedOutside(root string, row types.Job, projects []*types.Project, referrers func(file, declaration string) map[string]int) []ReferencedFile {
	if row.ReadOnly || len(row.WritePaths) == 0 || referrers == nil {
		return nil
	}
	found := map[string]*ReferencedFile{}
	add := func(entry string, refs map[string]int) {
		for file, n := range refs {
			if slices.ContainsFunc(row.WritePaths, func(w string) bool { return covers(w, file) }) || generatedFile(projects, file) {
				continue
			}
			f, ok := found[file]
			if !ok {
				f = &ReferencedFile{File: file}
				found[file] = f
			}
			f.Count += n
			if !slices.Contains(f.Uses, entry) {
				f.Uses = append(f.Uses, entry)
			}
		}
	}
	for _, entry := range row.WritePaths {
		if p, decl := types.SplitClaim(entry); decl != "" {
			add(entry, referrers(path.Clean(p), decl))
			continue
		}
		for _, file := range writtenFiles(root, types.Job{WritePaths: []string{entry}, DenyPaths: row.DenyPaths}) {
			add(file, referrers(file, ""))
		}
	}
	if len(found) == 0 {
		return nil
	}
	out := make([]ReferencedFile, 0, len(found))
	for _, f := range found {
		out = append(out, *f)
	}
	slices.SortFunc(out, func(a, b ReferencedFile) int {
		if at, bt := types.IsTestPath(a.File), types.IsTestPath(b.File); at != bt {
			if at {
				return -1
			}
			return 1
		}
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return cmp.Compare(a.File, b.File)
	})
	return out
}

func generatedFile(projects []*types.Project, file string) bool {
	return slices.ContainsFunc(projects, func(p *types.Project) bool {
		for _, refs := range p.TargetOutputs {
			if slices.ContainsFunc(refs, func(ref types.OutputRef) bool { return ref.Rooted(p.Path).Match(file) }) {
				return true
			}
		}
		return false
	})
}

// NoSymbolIndex is the gap a referenced-outside answer carries when no symbol index
// answered: an empty list then reads the same as nothing outside using the write paths.
func NoSymbolIndex() string {
	return fmt.Sprintf("no symbol index answered; `%s` builds one", hint.GraphBuild)
}

// RenderReferencedOutside writes files as the block [Terms] renders, for a command that
// prints no terms. A non-empty gap says why the list is unknown and is written in its
// place. Nothing is written for no files and no gap.
func RenderReferencedOutside(w io.Writer, files []ReferencedFile, gap string) {
	renderBlock(w, referencedHeading, referencedLines(files, gap))
}

func referencedLines(files []ReferencedFile, gap string) []string {
	if gap != "" {
		return []string{"unknown, " + gap}
	}
	if len(files) == 0 {
		return nil
	}
	shown := files[:min(len(files), findingsLimit)]
	lines := make([]string, 0, len(shown)+2)
	for _, f := range shown {
		kind := "uses"
		if types.IsTestPath(f.File) {
			kind = "tests"
		}
		lines = append(lines, fmt.Sprintf("%s (%s %s, %d reference(s))", f.File, kind, strings.Join(f.Uses, ", "), f.Count))
	}
	if rest := len(files) - len(shown); rest > 0 {
		lines = append(lines, fmt.Sprintf("and %d more; -o json lists every one", rest))
	}
	return append(lines,
		"a change to what these use reaches them: add each one the change touches to the write paths, or ask the root to")
}
