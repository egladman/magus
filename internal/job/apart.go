package job

import (
	"path"
	"slices"

	"github.com/egladman/magus/types"
)

// HeldApart reports whether holder's hold leaves a job in checkout here alone: holder works
// in another checkout and one of paths (the declarations that meet) names only a declared
// output. Each branch regenerates its own copy, and integration regenerates once, so that
// hold protects nothing. Hand-written source always holds, as does any hold in one checkout,
// an untaken holder's, or one when projects answers nil. projects is read only once the
// checkouts differ. Fork, apply, the guard and `magus ls jobs` all ask this one question.
func HeldApart(here string, holder types.Job, projects func() []*types.Project, paths ...string) bool {
	there := WorkingCheckout(holder.CheckoutRoot, holder)
	if here == "" || there == "" || samePath(here, there) {
		return false
	}
	declared := projects()
	return slices.ContainsFunc(paths, func(p string) bool { return generatedEntry(declared, p) })
}

// generatedEntry reports whether every file the declaration entry names is a declared
// output. A glob counts only when it sits within an output glob that carves nothing out:
// a carved-out file is hand-maintained.
func generatedEntry(projects []*types.Project, entry string) bool {
	file, _ := types.SplitClaim(entry)
	file = path.Clean(file)
	if file == "." || file == "/" {
		return false
	}
	if !types.IsGlobMeta(file) {
		return generatedFile(projects, file)
	}
	return slices.ContainsFunc(projects, func(p *types.Project) bool {
		for _, refs := range p.TargetOutputs {
			if slices.ContainsFunc(refs, func(ref types.OutputRef) bool {
				g := ref.Rooted(p.Path)
				return len(g.Except) == 0 && types.PathWithin(file, g.Pattern)
			}) {
				return true
			}
		}
		return false
	})
}

// contendedOverlaps is overlaps without each pair whose shared ground [HeldApart] frees,
// and with each path dropped that meets the other side only there. A side's paths are
// only the declarations meeting the other's, so when all of one side's are apart, the
// whole ground is.
func contendedOverlaps(rows []types.Job, overlaps []types.JobOverlap, projects func() []*types.Project) []types.JobOverlap {
	row := func(id string) types.Job {
		i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
		if i < 0 {
			return types.Job{ID: id}
		}
		return rows[i]
	}
	var out []types.JobOverlap
	for _, o := range overlaps {
		a, b := row(o.JobA), row(o.JobB)
		apartA := func(p string) bool { return HeldApart(WorkingCheckout(a.CheckoutRoot, a), b, projects, p) }
		apartB := func(p string) bool { return HeldApart(WorkingCheckout(b.CheckoutRoot, b), a, projects, p) }
		pathsA := slices.DeleteFunc(slices.Clone(o.PathsA), apartA)
		pathsB := slices.DeleteFunc(slices.Clone(o.PathsB), apartB)
		if len(pathsA) == 0 || len(pathsB) == 0 {
			continue
		}
		o.PathsA, o.PathsB = pathsA, pathsB
		out = append(out, o)
	}
	return out
}

// workspaceProjects is the loaded workspace's projects, nil when the store has none.
func (s *Store) workspaceProjects() []*types.Project {
	ws, why := s.loadedWorkspace()
	if why != "" {
		return nil
	}
	return ws.Projects
}
