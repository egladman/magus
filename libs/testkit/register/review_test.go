package register_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/interp/bindings/gen/providerops"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

func TestReviewSpellAnswersThroughTheTypedProviderOps(t *testing.T) {
	at := types.ReviewTarget{ID: "482", Repo: "acme/acme", State: "merged", Author: "priya", Viewer: "eli"}
	threads := []types.ReviewComment{
		{ID: "t1", Path: "a.go", Line: 9, Author: "priya", Body: "why {this}?\n\"quoted\""},
		{ID: "t2", Root: "t1", Author: "eli", Body: "because", Outdated: true, DiffHunk: "@@ -1 +1 @@"},
	}
	drv, ok := project.DefaultSpellRegistry().Lookup(register.ReviewSpell(t, at, threads))
	require.True(t, ok)

	gotAt, exported, err := bindings.CallOp(t.Context(), drv, providerops.FindReview, types.Target{}, types.FindReviewRequest{})
	require.NoError(t, err)
	assert.True(t, exported)
	assert.Equal(t, at, gotAt)

	gotThreads, exported, err := bindings.CallOp(t.Context(), drv, providerops.ReviewThreads, types.Target{}, types.ReviewRef{})
	require.NoError(t, err)
	assert.True(t, exported)
	assert.Equal(t, threads, gotThreads)
}
