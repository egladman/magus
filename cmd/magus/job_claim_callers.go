package main

import (
	"cmp"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// expandCallerClaims replaces each callers request with one request per declaration
// holding an occurrence of the symbol, leaving out one row already holds, which the store
// would refuse. It lives here so internal/job never reads the symbol index; the store
// still proves every request it returns.
func expandCallerClaims(ctx context.Context, root string, row types.Job, reqs []types.JobClaimRequest) ([]types.JobClaimRequest, error) {
	if !slices.ContainsFunc(reqs, func(r types.JobClaimRequest) bool { return r.Callers != "" }) {
		return reqs, nil
	}
	ws, err := openWorkspaceForRead(ctx, root)
	if err != nil {
		return nil, err
	}
	res, err := vcs.Resolve(ctx, ws.Root(), "", types.VCSOptions{})
	if err != nil {
		return nil, fmt.Errorf("magus job apply: resolving the version control backend to place callers: %w", err)
	}
	if res.VCS == nil {
		return nil, exit.Usagef("magus job apply: version control is disabled here, so no caller of a symbol can be placed in a declaration")
	}
	declarations := func(file string) ([]types.RegionChange, error) {
		body, err := os.ReadFile(filepath.Join(ws.Root(), filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		return res.VCS.RegionsBetween(ctx, ws.Root(), file, nil, body)
	}
	var out []types.JobClaimRequest
	for _, req := range reqs {
		if req.Callers == "" {
			out = append(out, req)
			continue
		}
		name := strings.TrimSpace(req.Callers)
		g, err := loadRefsGraph(ctx, ws, globalCfg, false, name)
		if err != nil {
			return nil, err
		}
		id, err := callersSymbol(g, name)
		if err != nil {
			return nil, err
		}
		language := g.SymbolLanguage(id)
		if blind := renameBlindSpots(id, language, symbolCoverage(ctx, root, id, true)); len(blind) > 0 {
			return nil, callersRefusal(name, blind)
		}
		read, probed := workspacegraph.SymbolOccurrences(ctx, ws, ws.Root(), globalCfg, slog.Default(), strings.TrimPrefix(id, types.KindSymbol+":"))
		if !probed {
			return nil, exit.Usagef("magus job apply: callers of %s: cannot read the symbol indexes", name)
		}
		var unreadable []types.EditRefusal
		for _, gap := range knowledge.NarrowGaps(read.Unreadable, language) {
			unreadable = append(unreadable, types.EditRefusal{Reason: fmt.Sprintf("project %s: symbol index %s; rebuild it with `%s`", gap.Project.Path, gap.Describe(), hint.GraphBuild)})
		}
		if len(unreadable) > 0 {
			return nil, callersRefusal(name, unreadable)
		}
		refs, _ := g.Refs(id)
		var defined string
		if len(refs.Defs) > 0 {
			defined = refs.Defs[0].File
		}
		ident := cmp.Or(append(slices.Clone(read.Names), refs.Label)...)
		expanded, err := callerClaims(ident, goPackageName(ws.Root(), defined), path.Dir(defined), read.Files, declarations)
		if err != nil {
			return nil, err
		}
		for _, e := range expanded {
			if !heldClaim(row, e.Path) {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

// callersSymbol resolves name to the one symbol it names, refusing a name the index does
// not resolve or resolves to several.
func callersSymbol(g *knowledge.Graph, name string) (string, error) {
	id, ambiguous := pickSymbol(g, name)
	if len(ambiguous) > 0 {
		return "", exit.Usagef("magus job apply: callers %q names %d symbols defined in this workspace, so name one by id: %s",
			name, len(ambiguous), strings.Join(ambiguous, ", "))
	}
	if _, ok := g.Refs(id); !ok {
		return "", exit.Usagef("magus job apply: callers %q names no symbol the index holds; check the name, or bring the index current with `%s`", name, hint.GraphBuild)
	}
	return id, nil
}

func callersRefusal(name string, refused []types.EditRefusal) error {
	reasons := make([]string, len(refused))
	for i, r := range refused {
		reasons[i] = r.Reason
	}
	return exit.Usagef("magus job apply: callers of %s cannot be listed whole: %s", name, strings.Join(reasons, "; "))
}

// callerClaims is one request per declaration holding a non-definition occurrence, in file
// then line order. pkg and dir are the defining file's package name and directory: a
// caller outside dir uses `<pkg>.<ident>`, so that is what its proof names. declarations
// places every line of a file, as RegionsBetween does against an empty file.
func callerClaims(ident, pkg, dir string, files []types.SymbolOccurrenceFile, declarations func(file string) ([]types.RegionChange, error)) ([]types.JobClaimRequest, error) {
	var out []types.JobClaimRequest
	for _, f := range slices.SortedFunc(slices.Values(files), func(a, b types.SymbolOccurrenceFile) int { return cmp.Compare(a.File, b.File) }) {
		if f.Stale {
			return nil, staleCallers(f.File)
		}
		var regions []types.RegionChange
		for _, occ := range f.Occurrences {
			if occ.Definition {
				continue
			}
			if occ.Status != types.SymbolOccurrenceVerified {
				return nil, staleCallers(fmt.Sprintf("%s:%d", f.File, occ.Line))
			}
			if regions == nil {
				placed, err := declarations(f.File)
				if err != nil {
					return nil, fmt.Errorf("magus job apply: placing the declarations of %s: %w", f.File, err)
				}
				regions = placed
			}
			word, err := enclosingDeclaration(f.File, occ.Line, regions)
			if err != nil {
				return nil, err
			}
			because := "refs:" + ident
			if path.Dir(f.File) != dir && pkg != "" {
				because = "refs:" + pkg + "." + ident
			}
			req := types.JobClaimRequest{Path: strings.ReplaceAll(f.File, "#", `\#`) + "#" + word, Because: because}
			if !slices.Contains(out, req) {
				out = append(out, req)
			}
		}
	}
	return out, nil
}

func staleCallers(where string) error {
	return exit.Usagef("magus job apply: the symbol index is behind the tree at %s, so its callers cannot be placed; rebuild it with `%s`", where, hint.GraphBuild)
}

// enclosingDeclaration is the claim word for the declaration holding line of file: the
// shortest of the declared name, a method's `Recv) name` and the whole declaration line
// that names that declaration alone among the file's.
func enclosingDeclaration(file string, line int, regions []types.RegionChange) (string, error) {
	var decl string
	var all []string
	for _, r := range regions {
		loc := r.Location()
		if loc.Declaration != "" && loc.Declaration != types.Preamble && !slices.Contains(all, loc.Declaration) {
			all = append(all, loc.Declaration)
		}
		if r.Side == types.RegionNew && r.Lines[0] <= line && line <= r.Lines[1] {
			decl = loc.Declaration
		}
	}
	if decl == "" || decl == types.Preamble {
		return "", exit.Usagef("magus job apply: no declaration holds the occurrence at %s:%d, so no declaration claim covers it; claim %s in write_paths instead", file, line, file)
	}
	for _, word := range declarationWords(decl) {
		if word == "" {
			continue
		}
		if n := len(slices.DeleteFunc(slices.Clone(all), func(d string) bool { return !types.NamesDeclaration(word, d) })); n == 1 {
			return word, nil
		}
	}
	return "", exit.Usagef("magus job apply: the declaration holding %s:%d is not named alone by any claim of %s, so claim %s in write_paths instead", file, line, file, file)
}

var (
	declaredNameLine = regexp.MustCompile(`^(?:func\s*(?:\([^)]*\)\s*)?|type\s+|var\s+|const\s+)([A-Za-z_][A-Za-z0-9_]*)`)
	methodLine       = regexp.MustCompile(`^func\s*\([^)]*?([A-Za-z_][A-Za-z0-9_]*)(?:\[[^\]]*\])?\)\s*([A-Za-z_][A-Za-z0-9_]*)`)
)

func declarationWords(decl string) []string {
	decl = strings.TrimSpace(decl)
	var words []string
	if m := declaredNameLine.FindStringSubmatch(decl); m != nil {
		words = append(words, m[1])
	}
	if m := methodLine.FindStringSubmatch(decl); m != nil {
		words = append(words, m[1]+") "+m[2])
	}
	return append(words, decl)
}

// goPackageName is the package clause of the Go file rel under root, "" when it is not Go
// or does not parse.
func goPackageName(root, rel string) string {
	if path.Ext(rel) != ".go" {
		return ""
	}
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, filepath.FromSlash(rel)), nil, parser.PackageClauseOnly)
	if err != nil {
		return ""
	}
	return f.Name.Name
}

// heldClaim says row already holds claim: by the claim itself, or by a write path covering
// its whole file.
func heldClaim(row types.Job, claim string) bool {
	file, _ := types.SplitClaim(claim)
	return slices.ContainsFunc(row.WritePaths, func(w string) bool {
		_, word := types.SplitClaim(w)
		return w == claim || word == "" && job.Covers(w, file)
	})
}
