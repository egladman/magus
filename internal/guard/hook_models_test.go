package guard

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
	typesagent "github.com/egladman/magus/types/agent"
)

// tieredWorkspace is a loaded workspace that declares set as its model tiers.
type tieredWorkspace struct {
	types.WorkspaceRepository
	set *typesagent.ModelTierSet
}

func (w tieredWorkspace) ModelTiers(context.Context) *typesagent.ModelTierSet { return w.set }

// The guard's model tiers open the workspace on the first ask and never again, so a spawn
// that never asks, one bound to no tiered row, pays nothing for the load.
func TestWorkspaceModelTiersOpenTheWorkspaceOnceAndOnlyWhenAsked(t *testing.T) {
	set, err := typesagent.NewModelTierSet(typesagent.ModelTierDeclaration{
		Enum: "ModelTier", Site: "magusfile.buzz:3", Cases: []string{"economy", "principal"},
		Bounds: []typesagent.ModelTierBounds{{}, {}}, Skills: []typesagent.SkillVariant{typesagent.SkillVariantFull, typesagent.SkillVariantShort},
	})
	require.NoError(t, err)
	cases := []struct {
		name string
		ws   types.WorkspaceRepository
		err  error
		want *typesagent.ModelTierSet
	}{
		{"a tiered workspace", tieredWorkspace{set: set}, nil, set},
		{"a workspace with no tier reader", struct{ types.WorkspaceRepository }{}, nil, nil},
		{"a workspace that will not load", nil, errors.New("magusfile.buzz does not load"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			tiers := workspaceModelTiers(t.Context(), func(context.Context) (types.WorkspaceRepository, error) {
				opened++
				return tc.ws, tc.err
			})
			assert.Zero(t, opened, "building the reader reads nothing")
			assert.Same(t, tc.want, tiers())
			assert.Same(t, tc.want, tiers())
			assert.Equal(t, 1, opened)
		})
	}
}

// The read sees the workspace the one open returned and the reader's ctx; an open that
// fails is T's zero value and never reaches the read.
func TestOnceInspectedOpensOnceAndReadsWhatOpened(t *testing.T) {
	type key struct{}
	ws := struct{ types.WorkspaceRepository }{}
	for _, tc := range []struct {
		name  string
		err   error
		want  string
		reads int
	}{
		{"an open workspace", nil, "read", 1},
		{"a workspace that will not load", errors.New("magusfile.buzz does not load"), "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(t.Context(), key{}, "judgment")
			opened, reads := 0, 0
			get := onceInspected(ctx, func(context.Context) (types.WorkspaceRepository, error) {
				opened++
				return ws, tc.err
			}, func(ctx context.Context, got types.WorkspaceRepository) string {
				reads++
				assert.Equal(t, ws, got)
				assert.Equal(t, "judgment", ctx.Value(key{}))
				return "read"
			})

			assert.Zero(t, opened, "building the reader reads nothing")
			assert.Equal(t, tc.want, get())
			assert.Equal(t, tc.want, get())
			assert.Equal(t, 1, opened)
			assert.Equal(t, tc.reads, reads)
		})
	}
}

// Outside a workspace the guard has no tier reader, and the tier rules have nothing to
// compare.
func TestGuardModelTiersIsNilOutsideAWorkspace(t *testing.T) {
	assert.Nil(t, testHookEnv().modelTiers(t.Context(), t.TempDir()))
}

func TestHarnessModelRowsOpenTheWorkspaceOnceAndOnlyWhenAsked(t *testing.T) {
	opened := 0
	rows := HarnessModelRows(t.Context(), t.TempDir(), func(context.Context) (types.WorkspaceRepository, error) {
		opened++
		return nil, errors.New("magusfile.buzz does not load")
	})

	assert.Zero(t, opened, "building the reader reads nothing")
	assert.Empty(t, rows(), "a workspace that will not load has no rows")
	assert.Empty(t, rows())
	assert.Equal(t, 1, opened)
}
