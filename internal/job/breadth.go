package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/text/plural"
	"github.com/egladman/magus/types"
)

// Workspace is what the workspace-aware checks of a job record read: the loaded projects
// and the VCS ignore filter.
type Workspace struct {
	Projects []*types.Project
	// Ignored returns the paths of its argument the VCS ignores.
	Ignored func([]string) ([]string, error)
	// Unavailable says why the workspace could not be loaded, empty when it was. A check
	// that needs it reports itself unchecked rather than passing.
	Unavailable string
}

// broadClaimProblems reports each write path of row that claims more than the workspace's
// jobs.max_claim_files or jobs.max_claim_projects allow, as WritePathTooBroad. A job's edit
// is one change a reviewer reads; a write path past either bound claims files nobody meant
// to edit, and blocks every sibling that needs one of them. A sweep that truly needs more
// says so in a deviation. The bounds come from the workspace's own magus.yaml, and one that
// cannot be read is a problem of the row rather than a default.
func broadClaimProblems(ctx context.Context, root string, row job.Job, ws Workspace, read SymbolReader) types.JobProblems {
	// An integration's claim is computed: what merged, plus what regenerates.
	if row.ReadOnly || row.Integrating() {
		return nil
	}
	cfg, err := config.LoadWorkspaceOnly(root)
	if err == nil {
		err = config.Validate(cfg)
	}
	if err != nil {
		return types.JobProblems{{
			Code:    types.WritePathTooBroad,
			Field:   "paths.write",
			Message: fmt.Sprintf("write path breadth was not judged: the claim bounds jobs.max_claim_files and jobs.max_claim_projects are unreadable: %v", err),
			Fix:     "fix the jobs section of magus.yaml, then fork again",
		}}
	}
	maxFiles, maxProjects := cfg.Jobs.MaxClaimFiles, cfg.Jobs.MaxClaimProjects
	projectOf := projectLocator(root, ws)
	var problems types.JobProblems
	var fix func(string) string
	for i, wp := range row.Paths.Write {
		file, decl := job.SplitClaim(wp)
		if decl != "" || !types.IsGlobMeta(file) {
			continue
		}
		files := writtenFiles(root, job.Job{Paths: job.Paths{Write: []string{wp}, Deny: row.Paths.Deny}})
		if ws.Ignored != nil {
			ignored, _ := ws.Ignored(files)
			files = slices.DeleteFunc(files, func(f string) bool { return slices.Contains(ignored, f) })
		}
		var projects []string
		for _, f := range files {
			if p := projectOf(f); !slices.Contains(projects, p) {
				projects = append(projects, p)
			}
		}
		var over []string
		if len(projects) > maxProjects {
			over = append(over, fmt.Sprintf("more than %s", plural.Count(maxProjects, "project", "projects")))
		}
		if len(files) > maxFiles {
			over = append(over, fmt.Sprintf("more than %s", plural.Count(maxFiles, "file", "files")))
		}
		if strings.HasPrefix(path.Clean(file), "**") {
			over = append(over, "a leading `**`, which reaches every directory of the workspace")
		}
		if len(over) == 0 {
			continue
		}
		if fix == nil {
			fix = broadClaimFix(ctx, root, row, read)
		}
		problems = append(problems, types.JobProblem{
			Code:    types.WritePathTooBroad,
			Field:   fmt.Sprintf("paths.write[%d]", i),
			Subject: wp,
			Message: fmt.Sprintf("write path %q matches %s in %s: %s",
				wp, plural.Count(len(files), "file", "files"), plural.Count(len(projects), "project", "projects"), strings.Join(over, ", ")),
			Fix: fix(wp),
		})
	}
	return problems
}

// broadClaimFix measures the narrowest cover once and returns the fix text for
// one broad write path. The fix names the cover as a ready paths.write array,
// then the deviation that keeps the path.
func broadClaimFix(ctx context.Context, root string, row job.Job, read SymbolReader) func(string) string {
	graphless := read == nil
	answering := read
	if read != nil {
		answering = func(ctx context.Context, name, signature string) (SymbolFact, bool) {
			fact, ok := read(ctx, name, signature)
			graphless = graphless || !ok
			return fact, ok
		}
	}
	cover := narrowestCover(ctx, root, row, answering)
	asksGraph := slices.ContainsFunc(row.Goals, func(g job.Goal) bool {
		return g.Kind == job.GoalKindSymbol && len(g.Symbols) > 0
	})
	return func(wp string) string {
		var b strings.Builder
		if len(cover) > 0 {
			listed, _ := codec.Marshal(cover)
			fmt.Fprintf(&b, "replace %q with the files the goals and criteria name: \"paths\": {\"write\": %s}", wp, listed)
		} else {
			fmt.Fprintf(&b, "replace %q with the files this job edits, each by name: the criteria and goals name no file the graph resolves", wp)
		}
		if asksGraph && graphless {
			b.WriteString(" (the knowledge graph did not answer the symbol goals, so only the criteria were read; `magus graph build` and fork again for a fuller list)")
		}
		deviation, _ := codec.Marshal(job.Deviation{Code: job.Code(types.WritePathTooBroad), Subject: wp, Reason: "<why this job needs all of it>"})
		fmt.Fprintf(&b, "; or keep it with the deviation %s", deviation)
		return b.String()
	}
}

// narrowestCover returns the write paths the row's own goals and criteria point at: the
// files defining its symbol goals with the Go tests beside them, then each existing file
// or `file#declaration` claim named in its criteria. A nil read or an unanswered symbol
// contributes nothing, so the cover is then the criteria's alone.
func narrowestCover(ctx context.Context, root string, row job.Job, read SymbolReader) []string {
	var cover []string
	add := func(p string) {
		if !slices.Contains(cover, p) {
			cover = append(cover, p)
		}
	}
	for _, g := range row.Goals {
		if g.Kind != job.GoalKindSymbol || read == nil {
			continue
		}
		for _, name := range g.Symbols {
			fact, ok := read(ctx, name, "")
			if !ok {
				continue
			}
			for _, f := range fact.DefinedIn {
				add(f)
				if path.Ext(f) == ".go" {
					add(path.Join(path.Dir(f), "*_test.go"))
				}
			}
		}
	}
	for _, token := range strings.Fields(row.Instructions()) {
		if p, ok := criteriaPath(root, token); ok {
			add(p)
		}
	}
	return cover
}

// criteriaPath reads one whitespace-split token of prose as a workspace path: the quoting
// and punctuation prose wraps it in trimmed, and a `:line` suffix dropped. It is a path
// only when it names a regular file under root, so a word that merely holds a slash or a
// dot is never one.
func criteriaPath(root, token string) (string, bool) {
	token = strings.TrimLeft(token, "`'\"([{<")
	token = strings.TrimRight(token, "`'\")]}>,;:.!?")
	if i := strings.IndexByte(token, ':'); i >= 0 {
		token = token[:i]
	}
	file, _ := job.SplitClaim(token)
	if file == "" || types.IsGlobMeta(file) || path.IsAbs(file) {
		return "", false
	}
	if clean := path.Clean(file); clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return token, true
}

// projectLocator returns the function naming the project a workspace-relative file sits
// in: the deepest of ws.Projects holding it, or, when the workspace did not load, the
// nearest project root on disk, read once per directory.
func projectLocator(root string, ws Workspace) func(string) string {
	if len(ws.Projects) > 0 {
		return func(file string) string {
			best := "."
			for _, p := range ws.Projects {
				dir := path.Clean(p.Path)
				if (dir == "." || file == dir || strings.HasPrefix(file, dir+"/")) && len(dir) > len(best) {
					best = dir
				}
			}
			return best
		}
	}
	byDir := map[string]string{}
	return func(file string) string {
		dir := path.Dir(file)
		if p, ok := byDir[dir]; ok {
			return p
		}
		p := enclosingProject(root, file)
		byDir[dir] = p
		return p
	}
}

// LoadedWorkspace is the part of an opened workspace a job record's validation reads.
type LoadedWorkspace interface {
	Root() string
	All() []*types.Project
}

// WorkspaceOf is the workspace at root as a job record's validation reads it, from loaded
// and the error that opening it returned. A workspace that will not load comes back
// Unavailable with failure's text for that error, and so does one listing no projects or
// rooted elsewhere than root, so the checks that need it say they did not run rather than
// pass or judge against the wrong tree.
func WorkspaceOf(ctx context.Context, root string, loaded LoadedWorkspace, err error, failure func(error) string) Workspace {
	switch {
	case err != nil:
		return Workspace{Unavailable: failure(err)}
	case root != "" && filepath.Clean(loaded.Root()) != filepath.Clean(root):
		return Workspace{Unavailable: fmt.Sprintf("the loaded workspace is %s, not %s", loaded.Root(), root)}
	case len(loaded.All()) == 0:
		return Workspace{Unavailable: fmt.Sprintf("the workspace at %s lists no projects", loaded.Root())}
	}
	return Workspace{Projects: loaded.All(), Ignored: VCSIgnored(ctx, loaded.Root())}
}

// LoadFailureText is err's text. A binary too old for the tree also names the tree's own
// ./magus, own, when there is one.
func LoadFailureText(err error, own string) string {
	if !errors.Is(err, types.WorkspaceNeedsNewerMagus) || own == "" {
		return err.Error()
	}
	return fmt.Sprintf("%s\nThis checkout has its own build: rerun the command with `%s`", err, own)
}
