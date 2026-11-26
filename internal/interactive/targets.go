package interactive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
)

// RunSelection is how one run's project selection is named on the command line: the
// positionals, the --skip refs subtracted from them, and the directory they anchor
// against. A struct rather than three parameters because the first two are same-typed
// and adjacent, and transposing them is SILENT: the run would then cover exactly the
// projects the caller asked to exclude.
type RunSelection struct {
	// Projects are the positional project args, empty when the caller named none.
	Projects []string
	// Skips are the --skip refs, subtracted after the selection resolves.
	Skips []string
	// Cwd is the caller's working directory (the client's, for an adopted run); it anchors relative project args and the cwd-scope lookup. Resolving
	// cwd-scope against the server's own os.Getwd() is exactly the mix-up that let a
	// server adopt a run for an unrelated workspace and pass it vacuously, so the cwd is
	// threaded in rather than read here.
	Cwd string
	// Rooted is a run whose --root named the workspace: a relative project arg or skip
	// anchors at the root whatever Cwd is. Cwd still scopes a run naming no project.
	Rooted bool
}

// ResolveTargets resolves targets from the workspace: by path, explicit args, cwd-scope, or all,
// then subtracts the projects sel.Skips names.
//
// The subtraction happens here, at the one place the selection is finalized, so the run
// header, --dry-run and the fan-out all describe the same set.
func ResolveTargets(ctx context.Context, ws types.WorkspaceRepository, t types.Target, sel RunSelection) ([]types.Target, string, error) {
	anchor := "."
	if !sel.Rooted {
		anchor = currentWorkingDirectoryAnchor(ws.Root(), sel.Cwd)
	}

	// The four ways a selection is named, in precedence order.
	var (
		targets []types.Target
		source  string
		err     error
	)
	switch {
	case t.Path != "":
		resolved, rerr := file.ResolveProject(ctx, t.Path, anchor)
		if rerr != nil {
			return nil, "", rerr
		}
		t.Path = resolved
		targets, err = ws.ExpandPath(t)
	case len(sel.Projects) > 0:
		for _, arg := range sel.Projects {
			resolved, rerr := file.ResolveProject(ctx, arg, anchor)
			if rerr != nil {
				return nil, "", rerr
			}
			expanded, eerr := ws.ExpandPath(types.Target{Path: resolved, Name: t.Name})
			if eerr != nil {
				return nil, "", eerr
			}
			targets = append(targets, expanded...)
		}
	default:
		// cwd-scope: run only the project containing the caller's cwd, when it is inside
		// one. Keyed on the explicit cwd, never this process's working directory.
		if sel.Cwd != "" {
			if p, ok := ws.Where(sel.Cwd); ok {
				targets, source = []types.Target{{Path: p.Path, Name: t.Name}}, "cwd"
				break
			}
		}
		targets, err = ws.ExpandPath(t)
	}
	if err != nil {
		return nil, "", err
	}

	// Named on the command line, rather than inferred from the cwd or fanned out over
	// the workspace. That is what makes skipping one of these projects a contradiction
	// instead of a preference, so the value is named rather than computed in the call.
	explicit := t.Path != "" || len(sel.Projects) > 0
	targets, err = subtractSkipped(ctx, ws, t.Name, targets, sel.Skips, anchor, explicit)
	if err != nil {
		return nil, "", err
	}
	return targets, source, nil
}

// subtractSkipped removes the projects skips names from an already-resolved selection.
// A skip reference goes through the same resolver a positional does, so the two spellings
// cannot drift apart. explicit reports whether the selection was named on the command line
// rather than inferred from the cwd or fanned out over the workspace.
//
// Every refusal here exists because the flag's job is to GATE a set, and a skip that
// quietly matches nothing un-gates a project with no one the wiser. A reference no project
// matches is a typo, and a reference the caller also named explicitly is a contradiction
// rather than a preference. Emptying the selection would otherwise surface downstream as
// "workspace has no projects to run target".
func subtractSkipped(ctx context.Context, ws types.WorkspaceRepository, targetName string, targets []types.Target, skips []string, anchor string, explicit bool) ([]types.Target, error) {
	if len(skips) == 0 {
		return targets, nil
	}
	skip := make(map[string]bool, len(skips))
	for _, arg := range skips {
		// A glob subtracts every workspace project it matches, in the same doublestar
		// dialect declared sources use. It matches workspace-relative project paths
		// directly (never anchored to the cwd, because "libs/*" should mean the same
		// thing from anywhere), and a glob matching zero projects is the same typo an
		// unknown exact ref is.
		if strings.ContainsAny(arg, "*?[") {
			matched := false
			for _, p := range ws.All() {
				if ok, err := doublestar.Match(arg, p.Path); err != nil {
					return nil, fmt.Errorf("run: --skip %s: %w", arg, err)
				} else if ok {
					skip[p.Path] = true
					matched = true
				}
			}
			if !matched {
				return nil, fmt.Errorf("run: --skip %s: no project matches the glob", arg)
			}
			continue
		}
		resolved, err := file.ResolveProject(ctx, arg, anchor)
		if err != nil {
			return nil, fmt.Errorf("run: --skip %s: %w", arg, err)
		}
		if resolved == "" || resolved == "/" {
			return nil, fmt.Errorf("run: --skip %s: name one project, there is no all-projects skip", arg)
		}
		// ExpandPath is the existence check, and the same one a positional gets:
		// an unknown project errors here with its did-you-mean.
		if _, err := ws.ExpandPath(types.Target{Path: resolved, Name: targetName}); err != nil {
			return nil, fmt.Errorf("run: --skip %s: %w", arg, err)
		}
		skip[resolved] = true
	}
	kept := make([]types.Target, 0, len(targets))
	for _, t := range targets {
		if !skip[t.Path] {
			kept = append(kept, t)
			continue
		}
		if explicit {
			return nil, fmt.Errorf("run: --skip %s contradicts naming it on the command line", t.Path)
		}
	}
	if len(targets) > 0 && len(kept) == 0 {
		return nil, errors.New("run: --skip removed every selected project, nothing would run")
	}
	return kept, nil
}

// FilterServedTargets keeps only the (project, target) pairs whose project actually
// defines the target, so a fan-out never runs (or misreports "[pass]" for) a project
// that lacks it. It errors when nothing serves the target: a single project in scope
// names that project (you asked for it explicitly), otherwise it reads as an unknown
// target across the selection. When some projects serve it and others don't, the ones
// that don't are dropped with a warning: the tolerant multi-project behavior.
func FilterServedTargets(ctx context.Context, ws types.WorkspaceRepository, targets []types.Target, targetName string) ([]types.Target, error) {
	return applyTargetFilter(ctx, targets, targetName, DefinesTarget(ctx, ws), func(path string) string { return projectLabel(ws, path) })
}

// applyTargetFilter is the pure policy behind FilterServedTargets: partition targets by
// whether their project defines the target, error when none do (single project named vs
// unknown-across-selection), otherwise drop the undefined ones with a warning. Split out
// so the fault-tolerance matrix is testable without a live workspace.
func applyTargetFilter(ctx context.Context, targets []types.Target, targetName string, defines func(path, target string) bool, label func(path string) string) ([]types.Target, error) {
	var served, skipped []types.Target
	for _, t := range targets {
		if defines(t.Path, t.Name) {
			served = append(served, t)
		} else {
			skipped = append(skipped, t)
		}
	}
	if len(served) == 0 {
		if len(targets) == 1 {
			return nil, fmt.Errorf("run: target %q is not defined in project %s", targetName, label(targets[0].Path))
		}
		return nil, fmt.Errorf("run: target %q is not defined in any of the %d selected projects", targetName, len(targets))
	}
	if len(skipped) > 0 {
		names := make([]string, 0, len(skipped))
		for _, t := range skipped {
			names = append(names, label(t.Path))
		}
		slog.With(attr.Component("run")).WarnContext(ctx, "target not defined in some selected projects; skipping them",
			slog.String("target", targetName), slog.String("skipped", strings.Join(names, ", ")))
	}
	return served, nil
}

// DefinesTarget returns a predicate reporting whether a project defines a target,
// covering BOTH sources of runnable targets: magusfile-declared targets (the target
// graph's nodes) and spell ops (each bound spell's Targets). Neither set alone is
// complete: the magusfile spell exposes no ops, and spell ops are not graph nodes.
func DefinesTarget(ctx context.Context, ws types.WorkspaceRepository) func(path, target string) bool {
	byProject := map[string]map[string]bool{}
	add := func(path, name string) {
		set := byProject[path]
		if set == nil {
			set = map[string]bool{}
			byProject[path] = set
		}
		set[name] = true
	}
	// DefinesTarget returns a bare predicate closure with no error path of its
	// own; a cancelled ctx here just drops the graph-derived entries from the
	// predicate (the spell-derived ones added below still make it usable), rather
	// than failing a caller that never signed up to handle this.
	if graph, err := ws.TargetGraph(ctx); err == nil {
		for _, p := range graph.Projects {
			for _, n := range p.Nodes {
				add(p.Path, n.Name)
			}
		}
	}
	for _, p := range ws.All() {
		for _, sp := range p.ResolvedSpells {
			for _, t := range sp.Targets() {
				add(p.Path, t)
			}
		}
	}
	return func(path, target string) bool { return byProject[path][target] }
}

// projectLabel renders a project's human name (never a bare ".") from its path.
func projectLabel(ws types.WorkspaceRepository, path string) string {
	if p := ws.Get(path); p != nil {
		return types.ProjectLabel(p.Path, p.Dir)
	}
	return types.ProjectLabel(path, "")
}

// currentWorkingDirectoryAnchor returns cwd as a slash path relative to root, the anchor for
// resolving relative project args. It falls back to "." (the workspace root)
// when cwd is empty, cannot be made relative to root, or lies OUTSIDE it.
//
// That last case is what `--root <path>` selects: the caller is standing
// somewhere else, and filepath.Rel happily hands back a "../.."-prefixed path,
// which every relative project ref then inherits and fails the escape check on.
// `magus --root <ws> run build .` reported `project path "." escapes workspace
// root from "../<dir>"` for exactly that reason. A relative ref is measured from
// the workspace the caller NAMED, so an outside cwd contributes nothing to it.
// A cwd inside the workspace is untouched, which is every invocation without --root.
func currentWorkingDirectoryAnchor(root, cwd string) string {
	if cwd == "" {
		return "."
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	rel, err := filepath.Rel(root, cwd)
	if err != nil {
		return "."
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "."
	}
	return rel
}
