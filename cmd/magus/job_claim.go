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

// applyClaim applies the one claim record records holds: each path proved against
// the job's diff before the store's lock and again under it, and the proofs printed.
func applyClaim(ctx context.Context, root string, records []types.Declaration) error {
	if len(records) != 1 {
		return exit.Usagef("magus job apply: a claim record applies alone, and this input holds %d records", len(records))
	}
	rec := records[0]
	if err := rec.Validate(); err != nil {
		return exit.Usagef("magus job apply: %s", err)
	}
	store, err := openJobs(ctx, root, jobOpen{DryRun: globalCfg.DryRun})
	if err != nil {
		return err
	}
	rows, err := store.List()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == rec.ID })
	if i < 0 {
		return exit.Usagef("magus job apply: nothing declares job %s, so nothing claims", rec.ID)
	}
	reqs, err := expandCallerClaims(ctx, root, rows[i], rec.Claim)
	if err != nil {
		return err
	}
	if len(reqs) == 0 {
		return exit.Usagef("magus job apply: %s already holds every declaration the callers name, so nothing claims", rec.ID)
	}
	asked := make([]string, len(reqs))
	for k, w := range reqs {
		asked[k], _ = types.SplitClaim(w.Path)
	}
	ws := jobWorkspace(ctx, root)
	facts := job.ObserveClaim(ctx, root, rows[i], asked, ws.Projects)
	stored, err := store.Claim(ctx, rec.ID, reqs, facts, globalCfg.Jobs, globalCfg.DryRun)
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames(os.Stdout, []string{stored.ID})
	case outputText:
		verb := "claims"
		if globalCfg.DryRun {
			verb = "would claim"
		}
		for _, w := range stored.Claims[len(stored.Claims)-len(reqs):] {
			fmt.Printf("%s %s: + %s (%s): %s\n", verb, stored.ID, w.Path, w.Evidence, w.Proof)
		}
		return nil
	default:
		return emitFormatted(opts, stored)
	}
}

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
		expanded, err := job.CallerClaims(ident, goPackageName(ws.Root(), defined), path.Dir(defined), read.Files, declarations)
		if err != nil {
			return nil, exit.Usagef("magus job apply: %v", err)
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
