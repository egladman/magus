// Package serviceaudit bridges magus's resolved projects to the pure
// near-duplicate detector in internal/identity. It enumerates the service
// targets across a set of projects, renders each one's argv (without executing),
// and reports clusters of near-duplicate services. Both the run path (scoped to a
// run's reachable projects) and `magus doctor` (whole workspace) use it.
package serviceaudit

import (
	"github.com/egladman/magus/internal/service/identity"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// collectMembers returns one detector Member per service target across projects,
// naming each "path:target" and rendering its charm-applied argv via the spell's
// command renderer. A service target whose spell cannot render a command (no
// renderer, or the render fails) is skipped: it carries no argv to compare.
//
// The distinct reason comes from the static ServiceView rather than the render, because
// the opt-out is declared on the op and the rendered argv carries no trace of it. A member
// built from the command alone silently drops it, and that is not a cosmetic loss: MGS5001
// then warns about a divergence its own documented remedy cannot silence, while doctor's
// stale-suppression check sees no distinct services at all and reports OK forever.
func collectMembers(projects []*types.Project, charms []string) []identity.Member {
	var members []identity.Member
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
				svc := spells.Service{Command: spells.Command{Bin: bin, Args: args}}
				if view, vok := s.ServiceView(target); vok && view != nil {
					svc.Distinct = view.Distinct
				}
				members = append(members, identity.Member{
					Name:    p.Path + ":" + target,
					Service: svc,
				})
			}
		}
	}
	return members
}

// NearDuplicates collects service members across projects and returns the
// near-duplicate clusters among them (mirrors identity.NearDuplicates over the
// rendered service commands).
func NearDuplicates(projects []*types.Project, charms []string) []identity.Cluster {
	return identity.NearDuplicates(collectMembers(projects, charms))
}

// UnusedDistinct returns the "path:target" names of services marked distinct whose
// suppression no longer silences any near-duplicate (see
// identity.UnusedDistinct) - stale reasons to prune.
func UnusedDistinct(projects []*types.Project, charms []string) []string {
	return identity.UnusedDistinct(collectMembers(projects, charms))
}
