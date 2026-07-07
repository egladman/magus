// Package serviceaudit bridges magus's resolved projects to the pure
// near-duplicate detector in internal/serviceident. It enumerates the service
// targets across a set of projects, renders each one's argv (without executing),
// and reports clusters of near-duplicate services. Both the run path (scoped to a
// run's reachable projects) and `magus doctor` (whole workspace) use it.
package serviceaudit

import (
	"github.com/egladman/magus/internal/serviceident"
	"github.com/egladman/magus/types"
)

// collectMembers returns one detector Member per service target across projects,
// naming each "path:target" and rendering its charm-applied argv via the spell's
// command renderer. A service target whose spell cannot render a command (no
// renderer, or the render fails) is skipped: it carries no argv to compare.
func collectMembers(projects []*types.Project, charms []string) []serviceident.Member {
	var members []serviceident.Member
	for _, p := range projects {
		for _, s := range p.ResolvedSpells {
			for _, target := range s.Targets() {
				if !s.IsServiceTarget(target) {
					continue
				}
				bin, args, ok, err := s.RenderCommand(target, charms)
				if err != nil || !ok {
					continue
				}
				members = append(members, serviceident.Member{
					Name:    p.Path + ":" + target,
					Service: types.Service{Command: types.Command{Bin: bin, Args: args}},
				})
			}
		}
	}
	return members
}

// NearDuplicates collects service members across projects and returns the
// near-duplicate clusters among them (mirrors serviceident.NearDuplicates over the
// rendered service commands).
func NearDuplicates(projects []*types.Project, charms []string) []serviceident.Cluster {
	return serviceident.NearDuplicates(collectMembers(projects, charms))
}

// UnusedDistinct returns the "path:target" names of services marked distinct whose
// suppression no longer silences any near-duplicate (see
// serviceident.UnusedDistinct) - stale reasons to prune.
func UnusedDistinct(projects []*types.Project, charms []string) []string {
	return serviceident.UnusedDistinct(collectMembers(projects, charms))
}
