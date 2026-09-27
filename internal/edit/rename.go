package edit

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// identifierRe is the name a rename may write: the identifier shape Go, TypeScript,
// Python and Rust share. Anything wider could write syntax into a call site.
var identifierRe = regexp.MustCompile(`^[\p{L}_$][\p{L}\p{N}_$]*$`)

// RenameTarget picks the symbol a rename acts on from the graph's matches for ref. An exact
// node ID wins. A bare name must be the label of exactly one symbol the workspace defines:
// two would rename whichever the graph ranked first, and a dependency's symbol has no
// definition here to rename.
func RenameTarget(ref string, matches []types.KnowledgeMatch, defined func(id string) bool) (string, error) {
	var named []string
	for _, m := range matches {
		if m.Kind != types.KindSymbol {
			continue
		}
		if m.ID == ref {
			if !defined(m.ID) {
				return "", fmt.Errorf("%s is not defined in this workspace, so there is no definition to rename", ref)
			}
			return m.ID, nil
		}
		if m.Label == ref && defined(m.ID) {
			named = append(named, m.ID)
		}
	}
	switch len(named) {
	case 0:
		return "", fmt.Errorf("no symbol defined in this workspace is named %q; `%s` lists the one it resolves to, and its id renames it", ref, hint.Refs.With(ref))
	case 1:
		return named[0], nil
	}
	slices.Sort(named)
	return "", fmt.Errorf("%q names %d symbols defined in this workspace; pass the id of one:\n  %s", ref, len(named), strings.Join(named, "\n  "))
}

// RenameSites derives the sites that rename from to to from a symbol's verified
// occurrences. It refuses rather than derives when the list cannot be trusted to be
// complete and exact: a stale file may be missing sites no check can see, an unverified
// site points at text that moved, and a site holding another spelling (a package's import
// path) is not one a name substitution can rewrite.
func RenameSites(files []types.SymbolOccurrenceFile, from, to string) ([]Site, []types.EditRefusal) {
	var refused []types.EditRefusal
	switch {
	case !identifierRe.MatchString(to):
		refused = append(refused, types.EditRefusal{Reason: fmt.Sprintf("%q is not an identifier", to)})
	case to == from:
		refused = append(refused, types.EditRefusal{Reason: fmt.Sprintf("the symbol is already named %q", to)})
	}
	var sites []Site
	for _, f := range files {
		if f.Stale {
			refused = append(refused, types.EditRefusal{Path: f.File, Reason: fmt.Sprintf(
				"changed after it was indexed, so it may hold sites the index never saw; refresh with `%s`", hint.GraphBuild)})
			continue
		}
		for _, occ := range f.Occurrences {
			at := types.EditRefusal{Path: f.File, Line: occ.Line, Col: occ.Column}
			switch {
			case occ.Status != types.SymbolOccurrenceVerified:
				at.Reason = fmt.Sprintf("the site is %s, not verified; refresh with `%s`", occ.Status, hint.GraphBuild)
			case occ.Text != from:
				at.Reason = fmt.Sprintf("the site spells the symbol %q, which renaming %q does not rewrite; edit it by hand", occ.Text, from)
			}
			if at.Reason != "" {
				refused = append(refused, at)
				continue
			}
			sites = append(sites, Site{
				Path:  f.File,
				Start: types.EditPosition{Line: occ.Line, Col: occ.Column},
				End:   types.EditPosition{Line: occ.EndLine, Col: occ.EndColumn},
				Old:   from,
				New:   to,
			})
		}
	}
	if len(sites) == 0 && len(refused) == 0 {
		refused = append(refused, types.EditRefusal{Reason: "the index records no occurrence of the symbol"})
	}
	return sites, refused
}
