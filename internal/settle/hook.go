package settle

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// Hook regenerates what the git operation op changed and stages the result, the settle
// hooks' half of the merge driver. run executes one `<target>:rw <projects...>`
// invocation; defines reports whether a project defines a target.
//
// Everything is bounded by what the operation changed. Its paths are classified against
// the workspace's declarations, and only the projects that own a changed source or
// output run their generate target, plus the exact targets the merge driver recorded
// for the generated files it kept a side of. An operation that touches no generator's
// input runs nothing: the Outcome's Ran is empty.
//
// A run or stage that fails leaves its regeneration owed in the git dir, where `magus
// doctor` reports it, and the Outcome still names what ran.
func Hook(ctx context.Context, m *magus.Magus, op vcs.HookOperation, defines func(path, target string) bool, run func(ctx context.Context, inv []string) error) (Outcome, error) {
	files, err := m.ClassifyFiles(ctx, op.Changed)
	if err != nil {
		return Outcome{}, err
	}
	owed, err := vcs.OwedRegenerations(ctx, m.Root())
	if err != nil {
		return Outcome{}, err
	}
	o := Outcome{op: op, kind: op.Kind}
	o.Ran = Invocations(generatorProjects(files), owed, defines)
	if len(o.Ran) == 0 {
		return o, nil
	}
	for _, inv := range o.Ran {
		if err := run(ctx, inv); err != nil {
			o.recordOwed(ctx, m, files)
			return o, fmt.Errorf("%s: %w", hint.Run.With(inv...), err)
		}
	}
	if o.Staged, err = stageRegenerated(ctx, m, op, o.Ran); err != nil {
		o.recordOwed(ctx, m, files)
		return o, err
	}
	if err := vcs.DropOwedRegenerations(ctx, m.Root(), owed); err != nil {
		return o, err
	}
	if op.CommitPending {
		if err := vcs.HookRecordSettledTree(ctx, m.Root(), op); err != nil {
			return o, err
		}
	}
	return o, nil
}

// generatorProjects are the projects whose declared source or output the operation
// changed, sorted. An output that moved counts too: it was regenerated against a base
// this operation replaced, or the merge driver kept one side of it.
func generatorProjects(files []types.FileEntry) []string {
	set := types.SourceProjects(files)
	for _, f := range files {
		if f.Role == "output" {
			for _, p := range f.OutputOf {
				set[p] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// Invocations groups the runs into `<target>:rw <projects...>` invocations,
// deepest projects first: the generate target of every project in projects that defines
// one, plus each owed record's own target. An ancestor's generators read what its nested
// projects generate (the root's knowledge graph indexes docs/ pages), and docs is not
// declared a dependency of the root, so one run across both could build the root first
// and index stale pages.
func Invocations(projects []string, owed []vcs.OwedRegeneration, defines func(path, target string) bool) [][]string {
	type group struct {
		depth  int
		target string
	}
	groups := map[group]map[string]bool{}
	add := func(project, target string) {
		g := group{projectDepth(project), target}
		if groups[g] == nil {
			groups[g] = map[string]bool{}
		}
		groups[g][project] = true
	}
	for _, p := range projects {
		if defines(p, "generate") {
			add(p, "generate")
		}
	}
	for _, o := range owed {
		if defines(o.Project, o.Target) {
			add(o.Project, o.Target)
		}
	}
	order := slices.SortedFunc(maps.Keys(groups), func(a, b group) int {
		return cmp.Or(cmp.Compare(b.depth, a.depth), cmp.Compare(a.target, b.target))
	})
	out := make([][]string, 0, len(order))
	for _, g := range order {
		out = append(out, append([]string{g.target + ":rw"}, slices.Sorted(maps.Keys(groups[g]))...))
	}
	return out
}

func projectDepth(path string) int {
	if path == "." || path == "" {
		return 0
	}
	return strings.Count(path, "/") + 1
}

// stageRegenerated stages every declared output the invocations rewrote and returns
// them. Limited to the outputs of the projects that ran, so a file already modified
// elsewhere is not swept into the merge.
func stageRegenerated(ctx context.Context, m *magus.Magus, op vcs.HookOperation, ran [][]string) ([]string, error) {
	rebuilt := map[string]bool{}
	for _, inv := range ran {
		for _, p := range inv[1:] {
			rebuilt[p] = true
		}
	}
	dirty, err := vcs.HookDirtyFiles(ctx, m.Root(), op)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, path := range dirty {
		producer := m.FindOutputProducer(filepath.Join(m.Root(), filepath.FromSlash(path)))
		if producer != nil && rebuilt[projectKey(producer)] {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil, nil
	}
	return paths, vcs.HookStage(ctx, m.Root(), op, paths)
}

// Outcome is what one hook run did, for the line it prints.
type Outcome struct {
	op   vcs.HookOperation
	kind string
	// Ran are the invocations Hook ran, or was running when it failed.
	Ran [][]string
	// Staged are the regenerated files Hook staged.
	Staged []string
}

// recordOwed leaves the runs that did not settle owed in the git dir, each with the
// changed outputs of its projects, so `magus doctor` reports them until a run does.
func (o Outcome) recordOwed(ctx context.Context, m *magus.Magus, files []types.FileEntry) {
	for _, inv := range o.Ran {
		target := strings.TrimSuffix(inv[0], ":rw")
		for _, project := range inv[1:] {
			var paths []string
			for _, f := range files {
				if f.Role == "output" && slices.Contains(f.OutputOf, project) {
					paths = append(paths, f.Path)
				}
			}
			_, _ = vcs.RecordOwedRegeneration(ctx, m.Root(), vcs.OwedRegeneration{Project: project, Target: target, Paths: paths})
		}
	}
}

// Notice is the one line a settled hook prints: what changed, what ran, and what the
// output's fate is. fold is FoldCommand's answer.
func (o Outcome) Notice(hook, fold string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "magus: this %s changed generator inputs; ran %s", o.kind, o.Command())
	switch {
	case len(o.Staged) == 0:
		b.WriteString("; the generated output was already current")
	case hook == vcs.HookPreMergeCommit:
		fmt.Fprintf(&b, "; staged %d regenerated file(s) into the merge, so the commit is left to you: `git commit` concludes it", len(o.Staged))
	case o.op.CommitPending:
		fmt.Fprintf(&b, "; staged %d regenerated file(s) into this commit", len(o.Staged))
	case fold != "":
		fmt.Fprintf(&b, "; staged %d regenerated file(s); fold them into HEAD with `%s`", len(o.Staged), fold)
	default:
		fmt.Fprintf(&b, "; staged %d regenerated file(s); HEAD may already be pushed, so commit them as a new commit", len(o.Staged))
	}
	return b.String()
}

// Command is the shell command that repeats Ran.
func (o Outcome) Command() string {
	runs := make([]string, len(o.Ran))
	for i, inv := range o.Ran {
		runs[i] = hint.Run.With(inv...)
	}
	return strings.Join(runs, " && ")
}

// FoldCommand is how to fold staged output into HEAD: an amend when HEAD is an unpushed
// git commit, "" when it may already be published, no VCS resolves, or the backend
// cannot say.
func FoldCommand(ctx context.Context, m *magus.Magus) string {
	res, err := vcs.Resolve(ctx, m.Root(), "", m.VCSOptions())
	if err != nil {
		return ""
	}
	driver := res.VCS
	if driver == nil || driver.Name() != "git" {
		return ""
	}
	head, err := driver.Metadata(ctx, m.Root())
	if err != nil {
		return ""
	}
	if pushed, known, err := driver.CommitPushed(ctx, m.Root(), head.ID); err != nil || !known || pushed {
		return ""
	}
	return "git commit --amend --no-edit"
}
