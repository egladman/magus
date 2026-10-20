package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

func TestSpawnUnvalidatedRefusesARowNothingChecked(t *testing.T) {
	const title = "orchestrator/impl guard-facts"
	row := types.Job{ID: "guard-facts", State: types.StateDeclared, WritePaths: []string{"a/**"}}
	spawn := func(t *testing.T, description string, validated int64) types.GuardJudgment {
		t.Helper()
		ctx, _ := fleetFixture(t, row)
		at := hookLocation(ctx, Dependencies{})
		store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: at.cacheDir, Root: at.workspace})
		_, err := store.Update(ctx, row.ID, func(u *types.Job) { u.Validated = validated })
		require.NoError(t, err)
		return Judge(ctx, Dependencies{}, Request{Input: hookJSON(t, map[string]any{
			"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "Agent",
			"tool_input": map[string]any{"description": description, "prompt": "Carry the job to its done-when."},
		}), Host: "claude-code"})
	}

	t.Run("an unstamped row is refused with the fix", func(t *testing.T) {
		v := spawn(t, title, 0)
		require.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleSpawnUnvalidated)}, judgedAs(v), v.Context)
		assert.Contains(t, v.Reason, "job guard-facts carries no `validated` stamp")
		assert.Contains(t, v.Reason, "magus job fork guard-facts --dry-run")
		assert.Contains(t, v.Reason, "magus job apply -f <record>")
	})

	t.Run("a stamped row passes", func(t *testing.T) {
		v := spawn(t, title, 1)
		assert.NotEqual(t, string(denyRuleSpawnUnvalidated), v.Rule)
	})

	t.Run("a spawn naming no row is untouched", func(t *testing.T) {
		v := spawn(t, "explore the guard", 0)
		assert.NotEqual(t, string(denyRuleSpawnUnvalidated), v.Rule)
	})
}
