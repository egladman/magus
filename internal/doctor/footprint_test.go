package doctor

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceIsAlsoOutputFindsOnlyTheOverlap(t *testing.T) {
	r := &runner{}

	t.Run("a target reading its own output fails and names the path", func(t *testing.T) {
		got := r.checkSourceIsAlsoOutput([]*types.Project{{
			Path:          ".",
			TargetInputs:  map[string][]types.InputRef{"badge": {{Glob: "src/*.go"}, {Glob: "assets/badge.svg"}}},
			TargetOutputs: map[string][]types.OutputRef{"badge": {{Glob: "assets/badge.svg"}}},
		}})
		assert.Equal(t, types.DoctorFail, got.Status)
		require.Len(t, got.Details, 1, "only the overlapping path is a finding, not every declared input")
		assert.Contains(t, got.Details[0], "assets/badge.svg")
		assert.NotContains(t, got.Details[0], "src/*.go")
	})

	t.Run("reading one target's output from a DIFFERENT target is fine", func(t *testing.T) {
		got := r.checkSourceIsAlsoOutput([]*types.Project{{
			Path:          ".",
			TargetInputs:  map[string][]types.InputRef{"lint": {{Glob: "assets/badge.svg"}}},
			TargetOutputs: map[string][]types.OutputRef{"badge": {{Glob: "assets/badge.svg"}}},
		}})
		assert.Equal(t, types.DoctorOK, got.Status,
			"the ordinary producer/consumer pair, which ctx.needs sequences and the cache keys correctly")
	})

	t.Run("the same glob owned by different projects does not collide", func(t *testing.T) {
		got := r.checkSourceIsAlsoOutput([]*types.Project{{
			Path:          ".",
			TargetInputs:  map[string][]types.InputRef{"badge": {{Project: "docs", Glob: "assets/badge.svg"}}},
			TargetOutputs: map[string][]types.OutputRef{"badge": {{Project: ".", Glob: "assets/badge.svg"}}},
		}})
		assert.Equal(t, types.DoctorOK, got.Status)
	})

	t.Run("a repeated declaration is reported once", func(t *testing.T) {
		got := r.checkSourceIsAlsoOutput([]*types.Project{{
			Path:          ".",
			TargetInputs:  map[string][]types.InputRef{"badge": {{Glob: "assets/badge.svg"}, {Glob: "assets/badge.svg"}}},
			TargetOutputs: map[string][]types.OutputRef{"badge": {{Glob: "assets/badge.svg"}}},
		}})
		assert.Len(t, got.Details, 1)
	})
}
