package settle

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// The groups a dirty path can be left out by, in the order lsFilesGroups reports a path
// that falls in several.
const (
	LsGroupHeld        = "held"
	LsGroupStaged      = "staged"
	LsGroupUntracked   = "untracked"
	LsGroupUnclaimed   = "unclaimed"
	LsGroupUnexplained = "unexplained"
	LsGroupGenerated   = "generated"
)

type lsFilesGroup struct {
	name string
	has  func(lsFilesPath) bool
}

var lsFilesGroups = []lsFilesGroup{
	{LsGroupHeld, func(p lsFilesPath) bool { return p.Held != "" }},
	{LsGroupStaged, func(p lsFilesPath) bool { return p.Staged }},
	{LsGroupUntracked, func(p lsFilesPath) bool { return p.Untracked }},
	{LsGroupUnclaimed, func(p lsFilesPath) bool { return p.Role != "source" && p.Role != "output" }},
	{LsGroupUnexplained, func(p lsFilesPath) bool { return p.Role == "output" && p.Unexplained }},
	{LsGroupGenerated, func(p lsFilesPath) bool { return p.Role == "output" && !p.Unexplained }},
}

// lsFilesPath is one dirty path with every fact the selection reads.
type lsFilesPath struct {
	Path                           string
	Role                           string // types.FileEntry.Role
	Project                        string
	Held                           string // the other live job that writes it
	Untracked, Staged, Unexplained bool
}

// LsFilesFilter is what the caller asked for. Patterns and Exclude are write-path
// patterns already made workspace-relative; Include is keyed by group.
type LsFilesFilter struct {
	Patterns, Exclude, Projects []string
	Include                     map[string]bool
}

// selectLsFiles narrows paths by the filter, then leaves out every group the filter does
// not include. unmatched names each pattern and project that covered no dirty path.
func selectLsFiles(paths []lsFilesPath, f LsFilesFilter, version string) (list types.LsFiles, unmatched []string) {
	hit := map[string]bool{}
	var narrowed []lsFilesPath
	for _, p := range paths {
		if lsFilesKeeps(p, f, hit) {
			narrowed = append(narrowed, p)
		}
	}
	for _, want := range slices.Concat(f.Patterns, f.Projects) {
		if !hit[want] {
			unmatched = append(unmatched, want)
		}
	}

	list.Files = []string{}
	byGroup := map[string]*types.LsFilesExcluded{}
	for _, p := range narrowed {
		i := slices.IndexFunc(lsFilesGroups, func(g lsFilesGroup) bool { return g.has(p) && !f.Include[g.name] })
		if i < 0 {
			list.Files = append(list.Files, p.Path)
			continue
		}
		name := lsFilesGroups[i].name
		e := byGroup[name]
		if e == nil {
			e = &types.LsFilesExcluded{Group: name, Flag: "--include-" + name}
			byGroup[name] = e
		}
		e.Paths = append(e.Paths, p.Path)
		if name == LsGroupHeld {
			if e.Holders == nil {
				e.Holders = map[string]string{}
			}
			e.Holders[p.Path] = p.Held
		}
	}
	slices.Sort(list.Files)
	for _, g := range lsFilesGroups {
		e := byGroup[g.name]
		if e == nil {
			continue
		}
		slices.Sort(e.Paths)
		if g.name == LsGroupUnexplained {
			// No declared input moved, so this is ClassifyDrift's second fork: skew against a
			// differently-versioned magus, or a non-deterministic generator.
			code, msg := types.ClassifyDrift(false, version)
			e.Code, e.Message, e.URL = string(code), msg, types.CodeURL(code)
		}
		list.Excluded = append(list.Excluded, *e)
	}
	return list, unmatched
}

// lsFilesKeeps reports whether p passes the filter's patterns, exclusions and projects,
// marking in hit each pattern and project that covered it.
func lsFilesKeeps(p lsFilesPath, f LsFilesFilter, hit map[string]bool) bool {
	covered := len(f.Patterns) == 0
	for _, pat := range f.Patterns {
		if job.Covers(pat, p.Path) {
			hit[pat], covered = true, true
		}
	}
	if !covered || slices.ContainsFunc(f.Exclude, func(pat string) bool { return job.Covers(pat, p.Path) }) {
		return false
	}
	if len(f.Projects) == 0 {
		return true
	}
	owned := false
	for _, proj := range f.Projects {
		if path.Clean(proj) == path.Clean(cmp.Or(p.Project, ".")) {
			hit[proj], owned = true, true
		}
	}
	return owned
}

// LsFilesRefusal refuses an --include flag for a state the backend does not keep, rather
// than answering it with nothing.
func LsFilesRefusal(driver types.VCSDriver, include map[string]bool) error {
	if _, ok := driver.(types.StagedFileReporter); include[LsGroupStaged] && !ok {
		return fmt.Errorf("vcs ls-files: --include-staged: %s keeps no staging index, so no path is ever left out as staged", driver.Name())
	}
	if include[LsGroupUntracked] && !keepsUntracked(driver) {
		return fmt.Errorf("vcs ls-files: --include-untracked: %s tracks every file in the working copy, so no path is ever left out as untracked", driver.Name())
	}
	return nil
}

// keepsUntracked reports whether the backend has files present and not tracked. jj
// snapshots every file into the working-copy commit, and its TrackedFiles answers for
// the parent commit, which would call each new file untracked.
func keepsUntracked(driver types.VCSDriver) bool { return driver.Name() != "jj" }

// lsFilesFacts gathers what the selection reads about each dirty path.
func lsFilesFacts(ctx context.Context, ws types.WorkspaceRepository, res types.VCSResolution, root string, dirty []string, place func(root string) (job.Location, error)) ([]lsFilesPath, error) {
	if len(dirty) == 0 {
		return nil, nil
	}
	files, err := ws.ClassifyFiles(ctx, dirty)
	if err != nil {
		return nil, err
	}
	_, unexplained := types.SplitExplainedOutputs(files, types.SourcesChangedSinceBase(ctx, ws, res, root))
	untracked := map[string]bool{}
	if keepsUntracked(res.VCS) {
		tracked, err := res.VCS.TrackedFiles(ctx, root, dirty)
		if err != nil {
			return nil, fmt.Errorf("vcs ls-files: %w", err)
		}
		for _, p := range dirty {
			untracked[p] = !slices.Contains(tracked, p)
		}
	}
	var staged []string
	if sr, ok := res.VCS.(types.StagedFileReporter); ok {
		if staged, err = sr.StagedFiles(ctx, root); err != nil {
			return nil, fmt.Errorf("vcs ls-files: %w", err)
		}
	}
	held, err := heldForOtherLeases(ctx, place, root, dirty)
	if err != nil {
		return nil, err
	}
	out := make([]lsFilesPath, 0, len(files))
	for _, f := range files {
		out = append(out, lsFilesPath{
			Path:        f.Path,
			Role:        f.Role,
			Project:     f.Project,
			Held:        held[f.Path],
			Untracked:   untracked[f.Path],
			Staged:      slices.Contains(staged, f.Path),
			Unexplained: slices.Contains(unexplained, f.Path),
		})
	}
	return out, nil
}

// reportExcluded names on stderr, in every output format, each group left out and the
// flag that lists it.
func reportExcluded(ctx context.Context, groups []types.LsFilesExcluded) {
	for _, g := range groups {
		paths := slices.Clone(g.Paths)
		for i, p := range paths {
			if lease := g.Holders[p]; lease != "" {
				paths[i] = fmt.Sprintf("%s (live job %s writes it)", p, lease)
			}
		}
		msg := fmt.Sprintf("left out %d %s path(s); %s lists them: %s", len(paths), g.Group, g.Flag, strings.Join(paths, ", "))
		if g.Code != "" {
			msg += fmt.Sprintf("; %s: %s %s", g.Code, g.Message, g.URL)
		}
		slog.InfoContext(ctx, msg, attr.Notice(""), attr.Component("magus"))
	}
}

// WorkspaceRelPatterns is workspaceRelPaths for write-path patterns: the path half of
// each (jobtypes.SplitClaim) is made workspace-relative, and a literal `#` in it is escaped
// again so job.Covers reads it as part of the path.
func WorkspaceRelPatterns(root string, patterns []string) ([]string, error) {
	paths := make([]string, len(patterns))
	decls := make([]string, len(patterns))
	for i, p := range patterns {
		paths[i], decls[i] = jobtypes.SplitClaim(p)
	}
	rel, err := workspaceRelPaths(root, paths)
	if err != nil {
		return nil, err
	}
	for i, p := range patterns {
		rel[i] = strings.ReplaceAll(rel[i], "#", `\#`)
		if jobtypes.HasClaim(p) {
			rel[i] += "#" + decls[i]
		}
	}
	return rel, nil
}

// workspaceRelPaths turns the paths you typed into workspace-relative ones.
//
// ClassifyFiles, the declared-output globs, and the index write are all
// workspace-relative, but you run this from wherever you are. Passing the typed path
// through made `magus vcs ls-files foo.go` from a subdirectory miss, or address a different
// file, with no error: an unmatched path just reports as undeclared.
func workspaceRelPaths(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	// An empty root is a CALLER bug, not a path the user typed wrong. Left to run, it
	// reports every path as "outside the workspace at " with nothing after "at", which
	// blames the argument for a mistake it did not make.
	if root == "" {
		return nil, fmt.Errorf("no workspace root resolved, cannot place %q", paths[0])
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve working directory: %w", err)
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, p)
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("%q is outside the workspace at %s", p, root)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}

// ListFiles lists the dirty paths a commit of this change wants: every path the VCS reports
// dirty, narrowed and grouped by f. A pattern or project that covered nothing, and each group
// left out, is reported through slog.
func ListFiles(ctx context.Context, ws types.WorkspaceRepository, res types.VCSResolution, root string, f LsFilesFilter, place func(root string) (job.Location, error), version string) (types.LsFiles, error) {
	dirty, err := res.VCS.DirtyFiles(ctx, root, nil)
	if err != nil {
		return types.LsFiles{}, fmt.Errorf("vcs ls-files: list dirty files: %w", err)
	}
	facts, err := lsFilesFacts(ctx, ws, res, root, dirty, place)
	if err != nil {
		return types.LsFiles{}, err
	}
	list, unmatched := selectLsFiles(facts, f, version)
	for _, u := range unmatched {
		slog.WarnContext(ctx, u+" matches no dirty path", attr.Notice(""), attr.Component("magus"))
	}
	reportExcluded(ctx, list.Excluded)
	return list, nil
}
