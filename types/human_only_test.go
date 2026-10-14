package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

// TestHumanOnlyIndexRoundTrips pins the shape the knowledge manifest stores: a target's
// ref flattened beside its reason, and an index that declares nothing encoded as an empty
// object, so a manifest records "none declared" in two bytes.
func TestHumanOnlyIndexRoundTrips(t *testing.T) {
	ix := HumanOnlyIndex{
		Projects: []string{".", "evals"},
		Targets: []HumanOnlyTarget{{
			TargetRef: TargetRef{Project: ".", Target: "agent-bench-run"},
			Reason:    "launches paid sessions",
			SkipCache: true,
			ReachedBy: []TargetRef{{Project: ".", Target: "bench-all"}},
		}},
	}
	b, err := json.Marshal(ix)
	require.NoError(t, err)
	assert.JSONEq(t, `{"projects": [".", "evals"], "targets": [{"project": ".", "target": "agent-bench-run",
		"reason": "launches paid sessions", "skip_cache": true, "reached_by": [{"project": ".", "target": "bench-all"}]}]}`, string(b))

	var back HumanOnlyIndex
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, ix, back)

	none, err := json.Marshal(HumanOnlyIndex{})
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(none))
}
