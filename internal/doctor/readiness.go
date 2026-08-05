package doctor

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// checkReadinessProbes reports which tools this workspace's spells gate on, and what
// each gate asks.
//
// It runs the probes nowhere: doctor answers questions about the workspace, and
// forking `docker info` to render a report would make a read-only command depend on a
// daemon being up - the very coupling readiness exists to make legible. The runner
// enforces; this only says what WOULD be enforced, so someone hitting MGS3004 can see
// where the gate came from without reading a spell.
func (r *runner) checkReadinessProbes(projects []*types.Project) Check {
	type gate struct{ spell, tool, cmd string }
	var gates []gate
	seen := map[string]bool{}

	for _, p := range projects {
		for _, s := range p.ResolvedSpells {
			for _, tool := range s.ReadinessProbeTools() {
				probe, ok := s.ReadinessProbe(tool)
				if !ok {
					continue
				}
				key := s.Name() + "\x00" + tool
				if seen[key] {
					continue
				}
				seen[key] = true
				gates = append(gates, gate{
					spell: s.Name(),
					tool:  tool,
					cmd:   strings.Join(append([]string{probe.Bin}, probe.Args...), " "),
				})
			}
		}
	}
	if len(gates) == 0 {
		return Check{
			Name:    "tool readiness",
			Status:  types.DoctorOK,
			Message: "no spell gates an op on a tool being reachable",
		}
	}

	slices.SortFunc(gates, func(a, b gate) int {
		if a.spell != b.spell {
			return strings.Compare(a.spell, b.spell)
		}
		return strings.Compare(a.tool, b.tool)
	})

	details := make([]string, 0, len(gates))
	for _, g := range gates {
		details = append(details, fmt.Sprintf("%s: %s gated on `%s`", g.spell, g.tool, g.cmd))
	}
	return Check{
		Name:    "tool readiness",
		Status:  types.DoctorAdvice,
		Message: fmt.Sprintf("%d tool(s) gated on a readiness probe", len(gates)),
		Details: details,
	}
}
