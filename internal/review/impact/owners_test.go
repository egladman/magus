package impact

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/impact"
	"github.com/stretchr/testify/assert"
)

// TestImpactOwnersJoinOnlyTheReach pins both halves of the join: a project outside the
// blast radius is not this changeset's problem, and a project the lens cannot name an owner
// for is dropped rather than listed with a blank one, which reads as a lookup that failed.
func TestImpactOwnersJoinOnlyTheReach(t *testing.T) {
	own := types.Ownership{Projects: []types.OwnershipEntry{
		{Path: "root", Primary: "alice", PrimaryShare: 72, Authors: 4},
		{Path: "unrelated", Primary: "carol", PrimaryShare: 51, Authors: 9},
		{Path: "docs", Authors: 0},
	}}
	affected := []impact.Project{{Path: "root"}, {Path: "docs"}, {Path: "never-committed"}}

	got := listOwners(own, affected)
	assert.Equal(t, []projectOwner{{Project: "root", Primary: "alice", PrimaryShare: 72, Authors: 4}}, got)
}
