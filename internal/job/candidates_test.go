package job

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func unreferenced(id, kind, source string) types.UnreferencedEntry {
	return types.UnreferencedEntry{ID: id, Label: id, Kind: kind, Source: source}
}

func TestUnreferencedInPathsKeepsWhatAWritePathCovers(t *testing.T) {
	t.Parallel()

	all := []types.UnreferencedEntry{
		unreferenced("z", "Function", "internal/job/z.go:3"),
		unreferenced("claimed", "Function", "cmd/magus/job.go:9"),
		unreferenced("outside", "Function", "internal/graph/g.go:1"),
		unreferenced("a", "Struct", "internal/job/a.go:7"),
		unreferenced("method", "Method", "internal/job/a.go:20"),
		unreferenced("field", "Field", "internal/job/a.go:8"),
		unreferenced("test", "Function", "internal/job/a_test.go:5"),
	}
	row := types.Job{WritePaths: []string{"internal/job", "cmd/magus/job.go#Fork"}}

	got := UnreferencedInPaths(row, all)

	ids := make([]string, len(got))
	for i, e := range got {
		ids[i] = e.ID
	}
	assert.Equal(t, []string{"a", "claimed", "z"}, ids, "sorted; a claim covers its file; no methods, fields or tests")
}

func TestUnreferencedInPathsIsEmptyForARowThatWritesNothing(t *testing.T) {
	t.Parallel()

	all := []types.UnreferencedEntry{unreferenced("a", "Function", "internal/job/a.go:1")}
	assert.Nil(t, UnreferencedInPaths(types.Job{ReadOnly: true, WritePaths: []string{"internal/job"}}, all))
	assert.Nil(t, UnreferencedInPaths(types.Job{}, all))
}

func TestRenderDeletionCandidatesCapsAndCarriesTheCaveat(t *testing.T) {
	t.Parallel()

	var empty strings.Builder
	RenderDeletionCandidates(&empty, nil)
	assert.Empty(t, empty.String(), "nothing is written for none")

	var many []types.UnreferencedEntry
	for i := range DeletionCandidatesLimit + 3 {
		many = append(many, unreferenced(fmt.Sprintf("f%02d", i), "Function", "a.go:1"))
	}
	var out strings.Builder
	RenderDeletionCandidates(&out, many)
	got := out.String()

	assert.True(t, strings.HasPrefix(got, deletionHeading+"\n  f00 (function) at a.go:1\n"), got)
	assert.Contains(t, got, "f11 (function)")
	assert.NotContains(t, got, "f12 (function)")
	assert.Contains(t, got, "and 3 more; -o json lists every one")
	assert.Contains(t, got, "reflection, interface dispatch and consumers outside this workspace are invisible")
}
