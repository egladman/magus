package doctor

import (
	"fmt"
	"github.com/egladman/magus/spells"
	"os/exec"
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
func (r *runner) checkReadinessProbes(projects []*types.Project) types.DoctorCheck {
	type gate struct {
		spell, tool, cmd string
		probe            spells.Command
	}
	var gates []gate
	seen := map[string]bool{}

	for _, p := range projects {
		for _, s := range p.ResolvedSpells {
			for _, tool := range s.ToolNames() {
				t, ok := s.Tool(tool)
				if !ok || t.Ready.Bin == "" {
					continue
				}
				probe := t.Ready
				key := s.Name() + "\x00" + tool
				if seen[key] {
					continue
				}
				seen[key] = true
				gates = append(gates, gate{
					spell: s.Name(),
					tool:  tool,
					cmd:   strings.Join(append([]string{probe.Bin}, probe.Args...), " "),
					probe: probe,
				})
			}
		}
	}
	if len(gates) == 0 {
		return types.DoctorCheck{
			Name:    "tool-readiness",
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
	// Declaring a gate is not a finding. Starting at advice made every workspace whose
	// spells reach docker or podman permanently yellow, including one where --probe had
	// just confirmed every tool was up - and a level that cannot be cleared is one people
	// learn to skip past, taking the real ones with it. Only a failed probe lowers this.
	status := types.DoctorOK
	var down int
	for _, g := range gates {
		if !r.opts.probe {
			details = append(details, fmt.Sprintf("%s: %s gated on `%s`", g.spell, g.tool, g.cmd))
			continue
		}
		// --probe was asked for, so the gate is actually exercised. A tool that is
		// down is a FAIL rather than advice: the caller asked whether their
		// environment is ready, and the honest answer is no.
		if err := exec.CommandContext(r.runCtx(), g.probe.Bin, g.probe.Args...).Run(); err != nil {
			down++
			status = types.DoctorFail
			details = append(details, fmt.Sprintf("%s: %s NOT ready: `%s` failed", g.spell, g.tool, g.cmd))
			continue
		}
		details = append(details, fmt.Sprintf("%s: %s ready (`%s`)", g.spell, g.tool, g.cmd))
	}
	// Without --probe this check repeats what the spells DECLARE; with it, magus ran
	// every gate and is reporting what it saw. That is a different claim, and the
	// registry's default cannot know which one this run made.
	msg := fmt.Sprintf("%d tool(s) gated on a readiness probe", len(gates))
	evidence := types.EvidenceDeclared
	switch {
	case r.opts.probe && down > 0:
		msg = fmt.Sprintf("%d of %d gated tool(s) not ready", down, len(gates))
		evidence = types.EvidenceMeasured
	case r.opts.probe:
		msg = fmt.Sprintf("%d gated tool(s), all ready", len(gates))
		evidence = types.EvidenceMeasured
	}
	return types.DoctorCheck{Name: "tool-readiness", Status: status, Evidence: evidence, Message: msg, Details: details}
}
