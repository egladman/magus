package settle

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	"github.com/egladman/magus"
	"github.com/egladman/magus/types"
)

// Plan is what `magus vcs resolve` decided for each conflicted path, before it acts.
type Plan struct {
	// Keep are generated paths settled by taking a side and regenerating over it.
	Keep []string
	// Gone are paths whose deletion is the answer: both sides deleted them, or one did
	// and the workspace now ignores them.
	Gone []string
	// Rederive are paths magus maintains whose content is a function of the workspace, so
	// they are rebuilt rather than merged. Split from Keep so the report can say which.
	Rederive []string
	// Manual are the conflicts magus has no claim over; a human resolves them.
	Manual []string
	// Rebuild maps a target name to the projects that must run it to rebuild the kept
	// paths. Keyed by target so one `magus run generate` covers every project at once
	// instead of one run per file.
	Rebuild map[string][]string
}

// rebuiltProjects returns every project any rebuild target will run over, keyed by
// projectKey.
func (p Plan) rebuiltProjects() map[string]bool {
	out := map[string]bool{}
	for _, projects := range p.Rebuild {
		for _, key := range projects {
			out[key] = true
		}
	}
	return out
}

// projectKey is the one spelling of a project used to key the rebuild set, on BOTH the
// filling and the reading side.
//
// The PATH, never the label. Every nested project agrees on both spellings, so filling
// with paths and reading back with types.ProjectLabel looked correct, but the ROOT can
// never agree: ProjectLabel rejects "" and ".", resolving the root to its directory
// basename while the set holds ".". The lookup missed every time, leaving every
// root-owned regenerated output unstaged, which is the dirty tree Plan.Paths exists to
// prevent. One helper on both sides makes that divergence unrepresentable.
func projectKey(p *types.Project) string {
	if p.Path == "" {
		return "."
	}
	return p.Path
}

// PlanConflicts classifies every conflict without touching the tree.
//
// A path is settled automatically only when magus can name the target that rebuilds it.
// A VCS reports a conflict when BOTH sides changed a path, so taking a side always
// discards a real change: safe when a later run rewrites the file from source, data loss
// when nothing does.
func PlanConflicts(ctx context.Context, m *magus.Magus, resolver types.ConflictResolver, conflicts []types.Conflict) (Plan, error) {
	paths := make([]string, len(conflicts))
	for i, c := range conflicts {
		paths[i] = c.Path
	}
	// A generated file that is now ignored was removed from version control on purpose;
	// the other side carries a mechanical regeneration of it. Without this check both
	// sides look like declared outputs and the delete gets reverted every merge.
	ignored, err := resolver.IgnoredPaths(ctx, m.Root(), paths)
	if err != nil {
		return Plan{}, fmt.Errorf("vcs resolve: %w", err)
	}

	plan := Plan{Rebuild: map[string][]string{}}
	for _, c := range conflicts {
		abs := filepath.Join(m.Root(), filepath.FromSlash(c.Path))
		p := m.FindOutputProducer(abs)
		if p == nil {
			// Not a declared output. The merge-driver registration is the exception:
			// magus writes it, no target declares it, and it is re-derived.
			if types.IsMagusMaintained(c.Path) && c.Kind == types.ConflictKindContent {
				plan.Rederive = append(plan.Rederive, c.Path)
				continue
			}
			plan.Manual = append(plan.Manual, c.Path)
			continue
		}
		target, ok := rebuildTarget(p, abs)
		if !ok {
			plan.Manual = append(plan.Manual, c.Path)
			continue
		}
		switch {
		case c.Kind == types.ConflictKindBothDeleted:
			// Neither side has content. Record the removal and stop.
			plan.Gone = append(plan.Gone, c.Path)
			continue
		case c.Kind == types.ConflictKindDeleted && ignored[c.Path]:
			plan.Gone = append(plan.Gone, c.Path)
			continue
		case c.Kind == types.ConflictKindDeleted:
			// One side deleted a file that is STILL a tracked declared output. Keeping it
			// resurrects a deletion someone meant; dropping it loses an output the
			// workspace declares. Neither is magus's call.
			plan.Manual = append(plan.Manual, c.Path)
			continue
		}
		plan.Keep = append(plan.Keep, c.Path)
		// The project PATH, not its display label: this string becomes an argument to
		// `magus run <target> <project>`, and ProjectRef.Display renders the root as its
		// directory BASENAME so a bare "." never reaches a human-facing log. In a git
		// worktree that basename is the worktree's own directory name, which is not a
		// project any workspace knows, so resolve regenerated nothing and died with
		// `unknown project: "<worktree-dir>"`. Display's own doc draws this line: labels
		// for reading, the path for anything the user (or this code) feeds back to magus.
		proj := projectKey(p)
		if !slices.Contains(plan.Rebuild[target], proj) {
			plan.Rebuild[target] = append(plan.Rebuild[target], proj)
		}
	}
	slices.Sort(plan.Keep)
	slices.Sort(plan.Gone)
	slices.Sort(plan.Rederive)
	slices.Sort(plan.Manual)
	return plan, nil
}

// Paths returns everything to record: the kept paths, plus any OTHER declared
// output the regeneration rewrote.
//
// The second half is the normal case. A generate target writes every output its project
// declares, so recording only the conflicted paths leaves the rest modified and
// unrecorded: the dirty tree that makes `git rebase --continue` refuse.
//
// Limited to outputs of the projects that were rebuilt, so a file you had already
// modified elsewhere is not swept in.
func (p Plan) Paths(ctx context.Context, m *magus.Magus, driver types.VCSDriver) ([]string, error) {
	settled := map[string]bool{}
	for _, path := range slices.Concat(p.Keep, p.Rederive) {
		settled[path] = true
	}
	dirty, err := driver.DirtyFiles(ctx, m.Root(), nil)
	if err != nil {
		return nil, fmt.Errorf("list regenerated files: %w", err)
	}
	rebuilt := p.rebuiltProjects()
	for _, path := range dirty {
		if settled[path] {
			continue
		}
		producer := m.FindOutputProducer(filepath.Join(m.Root(), filepath.FromSlash(path)))
		if producer == nil || !rebuilt[projectKey(producer)] {
			continue
		}
		settled[path] = true
	}
	return slices.Sorted(maps.Keys(settled)), nil
}
