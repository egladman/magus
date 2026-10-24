package job

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// Workspace is what the workspace-aware checks of a job record read: the loaded projects
// and the VCS ignore filter.
type Workspace struct {
	Projects []*types.Project
	// Ignored returns the paths of its argument the VCS ignores.
	Ignored func([]string) []string
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
func broadClaimProblems(ctx context.Context, root string, row types.Job, ws Workspace, read SymbolReader) types.JobProblems {
	if row.ReadOnly {
		return nil
	}
	cfg, err := config.LoadWorkspaceOnly(root)
	if err == nil {
		err = config.Validate(cfg)
	}
	if err != nil {
		return types.JobProblems{{
			Code:    types.WritePathTooBroad,
			Field:   "write_paths",
			Message: fmt.Sprintf("write path breadth was not judged: the claim bounds jobs.max_claim_files and jobs.max_claim_projects are unreadable: %v", err),
			Fix:     "fix the jobs section of magus.yaml, then fork again",
		}}
	}
	maxFiles, maxProjects := cfg.Jobs.MaxClaimFiles, cfg.Jobs.MaxClaimProjects
	projectOf := projectLocator(root, ws)
	var problems types.JobProblems
	var fix func(string) string
	for i, wp := range row.WritePaths {
		file, decl := types.SplitClaim(wp)
		if decl != "" || !types.IsGlobMeta(file) {
			continue
		}
		files := writtenFiles(root, types.Job{WritePaths: []string{wp}, DenyPaths: row.DenyPaths})
		if ws.Ignored != nil {
			ignored := ws.Ignored(files)
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
			over = append(over, fmt.Sprintf("more than %s", plural(maxProjects, "project")))
		}
		if len(files) > maxFiles {
			over = append(over, fmt.Sprintf("more than %s", plural(maxFiles, "file")))
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
			Field:   fmt.Sprintf("write_paths[%d]", i),
			Subject: wp,
			Message: fmt.Sprintf("write path %q matches %s in %s: %s",
				wp, plural(len(files), "file"), plural(len(projects), "project"), strings.Join(over, ", ")),
			Fix: fix(wp),
		})
	}
	return problems
}

// broadClaimFix measures the narrowest cover once and returns the fix text for one broad
// write path: the cover as a ready write_paths array, then the deviation that keeps the path.
func broadClaimFix(ctx context.Context, root string, row types.Job, read SymbolReader) func(string) string {
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
	asksGraph := slices.ContainsFunc(row.Goals, func(g types.Goal) bool {
		return g.Kind == types.GoalKindSymbol && len(g.Symbols) > 0
	})
	return func(wp string) string {
		var b strings.Builder
		if len(cover) > 0 {
			listed, _ := json.Marshal(cover)
			fmt.Fprintf(&b, "replace %q with the files the goals and criteria name: \"write_paths\": %s", wp, listed)
		} else {
			fmt.Fprintf(&b, "replace %q with the files this job edits, each by name: the criteria and goals name no file the graph resolves", wp)
		}
		if asksGraph && graphless {
			b.WriteString(" (the knowledge graph did not answer the symbol goals, so only the criteria were read; `magus graph build` and fork again for a fuller list)")
		}
		deviation, _ := json.Marshal(types.Deviation{Code: types.WritePathTooBroad, Subject: wp, Reason: "<why this job needs all of it>"})
		fmt.Fprintf(&b, "; or keep it with the deviation %s", deviation)
		return b.String()
	}
}

// narrowestCover returns the write paths the row's own goals and criteria point at: the
// files defining its symbol goals with the Go tests beside them, then each existing file
// or `file#declaration` claim named in its criteria. A nil read or an unanswered symbol
// contributes nothing, so the cover is then the criteria's alone.
func narrowestCover(ctx context.Context, root string, row types.Job, read SymbolReader) []string {
	var cover []string
	add := func(p string) {
		if !slices.Contains(cover, p) {
			cover = append(cover, p)
		}
	}
	for _, g := range row.Goals {
		if g.Kind != types.GoalKindSymbol || read == nil {
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
	for _, token := range strings.Fields(row.Criteria) {
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
	file, _ := types.SplitClaim(token)
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

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
