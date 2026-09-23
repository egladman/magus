package knowledge

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// renameCheck reports a renamed symbol whose former name survives in the files that reference
// the new one: a comment, a string, or an identifier still spelling the old name.
//
// This is the RENAMED rule of this repository's TestCommentsNameSymbolsThatExist run forwards,
// and it keeps that rule's two narrowings, both measured there: the former name must be
// COMPOSED (two words or more, since one word is a word everybody uses), and UNIQUE (no symbol
// the index still defines carries its words in any casing, or a surviving mention may mean that
// one). Replayed over this repository's history, uniqueness by exact spelling reported
// describeTargets, a live CLI function, as a leftover of DescribeTargets; by words it does not.
type renameCheck struct{}

func (renameCheck) name() string { return types.CheckRenameLeftover }

func (renameCheck) run(x *namingIndex) []finding {
	if x.change.Read == nil || len(x.change.Renamed) == 0 {
		return nil
	}
	defined := map[string]bool{}
	for _, n := range x.g.nodes {
		if n.Kind == types.KindSymbol && n.Source != "" {
			defined[strings.Join(foldedWords(nameWords(n.Label)), " ")] = true
		}
	}
	var out []finding
	for _, subj := range x.subjects {
		old := x.change.Renamed[subj.id]
		oldWords := foldedWords(nameWords(old))
		if len(oldWords) < 2 || defined[strings.Join(oldWords, " ")] {
			continue
		}
		newWords := subj.folded
		files := x.referencingFiles(subj)
		var sites []string
		inFiles := 0
		for _, f := range files {
			text, ok := x.change.Read(f)
			if !ok {
				continue
			}
			found := leftoverLines(text, oldWords, newWords)
			if len(found) > 0 {
				inFiles++
			}
			for _, line := range found {
				sites = append(sites, fmt.Sprintf("%s:%d", f, line))
			}
		}
		if len(sites) == 0 {
			continue
		}
		noun := "lines"
		if len(sites) == 1 {
			noun = "line"
		}
		out = append(out, finding{
			subject: subj.id,
			score:   1,
			message: fmt.Sprintf("`%s` replaced `%s`, which %d %s in %d of the %d files referencing it still name",
				subj.label, old, len(sites), noun, inFiles, len(files)),
			details: capList(sites, conformanceExamples),
		})
	}
	return out
}

// referencingFiles is subj's own file and every file the index records referencing it, minus
// generated output, sorted.
func (x *namingIndex) referencingFiles(subj *namingDecl) []string {
	x.g.ensureAdj()
	files := []string{subj.file()}
	for _, e := range x.g.in[subj.id] {
		if e.Relation != types.RelationReferences {
			continue
		}
		if f, ok := strings.CutPrefix(e.Source, types.KindFile+":"); ok && !x.generated(f) && !slices.Contains(files, f) {
			files = append(files, f)
		}
	}
	slices.Sort(files)
	return files
}

// leftoverLines returns the 1-based lines of text holding an identifier whose words contain
// oldWords as a run but not newWords, which a rename to a longer name (EntryPointFrom to
// EntryPointFromContext) would otherwise match everywhere it succeeded.
func leftoverLines(text string, oldWords, newWords []string) []int {
	first, last := oldWords[0], oldWords[len(oldWords)-1]
	var out []int
	for i, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, first) || !strings.Contains(lower, last) {
			continue
		}
		for _, id := range identifiers(line) {
			ws := foldedWords(nameWords(id))
			if containsRun(ws, oldWords) && !containsRun(ws, newWords) {
				out = append(out, i+1)
				break
			}
		}
	}
	return out
}

// identifiers returns the identifier-shaped runs of s.
func identifiers(s string) []string {
	var out []string
	start := -1
	for i, r := range s {
		switch {
		case isIdentRune(r) && start < 0:
			start = i
		case !isIdentRune(r) && start >= 0:
			out = append(out, s[start:i])
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

func containsRun(ws, run []string) bool {
	for i := 0; i+len(run) <= len(ws); i++ {
		if slices.Equal(ws[i:i+len(run)], run) {
			return true
		}
	}
	return false
}
