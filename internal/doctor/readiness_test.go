package doctor

import (
	"fmt"
	"os/exec"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckReadinessProbes(t *testing.T) {
	gated := []*types.Project{{
		ResolvedSpells: []*spells.Spell{
			spells.NewSpell("compose", spells.WithTools(map[string]spells.Tool{
				"docker": {Ready: spells.Command{Bin: "magus-doctor-no-such-bin", Args: []string{"info"}}},
				"plain":  {},
			})),
		},
	}}

	t.Run("no gates", func(t *testing.T) {
		got := (&runner{}).checkReadinessProbes(t.Context(), []*types.Project{{}})
		assert.Equal(t, check.Check{
			Name:    "tool-readiness",
			Status:  check.OK,
			Message: "no spell gates an op on a tool being reachable",
		}, got)
	})

	// Doctor answers questions about the workspace, so listing the gate is the default
	// and forking the probe is opt-in. Declaring a gate is not a finding, so the listing
	// is OK: an advice nobody can clear is one every docker workspace carries forever.
	t.Run("lists the gate without running it", func(t *testing.T) {
		got := (&runner{}).checkReadinessProbes(t.Context(), gated)
		assert.Equal(t, check.Check{
			Name:     "tool-readiness",
			Status:   check.OK,
			Evidence: check.EvidenceDeclared,
			Message:  "1 tool(s) gated on a readiness probe",
			Details:  []string{"compose: docker gated on `magus-doctor-no-such-bin info`"},
		}, got)
	})

	// The same spell reached through two projects is one gate, not two.
	t.Run("deduplicates across projects", func(t *testing.T) {
		got := (&runner{}).checkReadinessProbes(t.Context(), append(gated, gated[0]))
		assert.Len(t, got.Details, 1)
	})

	// --probe means the caller asked whether their environment is ready, and the
	// honest answer for a tool that is down is no.
	t.Run("probing a tool that is down fails", func(t *testing.T) {
		got := (&runner{opts: options{probe: true}}).checkReadinessProbes(t.Context(), gated)
		require.Len(t, got.Details, 1)
		// The failed probe's own wording is pinned by substring below.
		assert.Equal(t, check.Check{
			Name:     "tool-readiness",
			Status:   check.Fail,
			Evidence: check.EvidenceMeasured,
			Message:  "1 of 1 gated tool(s) not ready",
			Details:  got.Details,
		}, got)
		assert.Contains(t, got.Details[0], "docker NOT ready")
	})

	// And the other half of that claim, which had no way to be reported: everything the
	// caller asked about answered, so the check passes rather than filing "0 of 1 not
	// ready" as advice.
	t.Run("probing a tool that is up passes", func(t *testing.T) {
		bin, err := exec.LookPath("true")
		if err != nil {
			t.Skip("no `true` on PATH to stand in for a healthy gate")
		}
		up := []*types.Project{{
			ResolvedSpells: []*spells.Spell{
				spells.NewSpell("compose", spells.WithTools(map[string]spells.Tool{
					"docker": {Ready: spells.Command{Bin: bin}},
				})),
			},
		}}
		got := (&runner{opts: options{probe: true}}).checkReadinessProbes(t.Context(), up)
		assert.Equal(t, check.Check{
			Name:     "tool-readiness",
			Status:   check.OK,
			Evidence: check.EvidenceMeasured,
			Message:  "1 gated tool(s), all ready",
			Details:  []string{fmt.Sprintf("compose: docker ready (`%s`)", bin)},
		}, got)
	})
}
