package impact

import (
	"fmt"
	"testing"

	"github.com/egladman/magus/types/diff"
	"github.com/egladman/magus/types/impact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestImpactListsReportWhatTheyLeftOff guards the bound on every section list. A reach of
// sixty projects is a real answer and an unreadable one, and a list that stops without saying
// so reads as the whole answer.
func TestImpactListsReportWhatTheyLeftOff(t *testing.T) {
	r := &reachSummary{Seeds: 1, Rebuilds: 14}
	for i := 0; i < 14; i++ {
		r.Projects = append(r.Projects, reachedProject{Path: fmt.Sprintf("p%d", i)})
	}
	lines := reachLines(r)
	require.Len(t, lines, 1+listCap+1)
	assert.Equal(t, "      and 4 more", lines[len(lines)-1])
}

// TestImpactReachRendersWhatTheDiffAlreadyKnew guards against the reach section growing its
// own idea of the blast radius: types.Diff has carried these two fields all along and this
// section only prints them.
func TestImpactReachRendersWhatTheDiffAlreadyKnew(t *testing.T) {
	assert.Nil(t, computeReach(diff.Diff{}), "an empty closure is a state, not a lookup failure")

	r := computeReach(diff.Diff{
		SeedProjects:     []string{"root"},
		AffectedProjects: []impact.Project{{Path: "root", Seed: true, Files: []string{"a.go", "b.go"}}, {Path: "docs"}},
	})
	assert.Equal(t, &reachSummary{
		Seeds:    1,
		Rebuilds: 2,
		Projects: []reachedProject{{Path: "root", Seed: true, Files: 2}, {Path: "docs"}},
	}, r)
}
