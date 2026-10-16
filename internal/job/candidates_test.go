package job

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestReferencedOutsideNamesTheTestAndRegistryOneHopOut(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "job"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal", "job", "a.go"), []byte("package job\n"), 0o644))
	row := types.Job{WritePaths: []string{"types/conformance.go", "cmd/magus/job.go#Fork", "internal/job"}}
	projects := []*types.Project{{Path: ".", TargetOutputs: map[string][]types.OutputRef{"mocks": {{Glob: "**/mocks_test.go"}}}}}
	asked := map[string]bool{}
	referrers := func(file, declaration string) map[string]int {
		asked[file+"#"+declaration] = true
		switch file + "#" + declaration {
		case "types/conformance.go#":
			return map[string]int{
				"types/conformance.go":                4,
				"cmd/magus-utils/boundary_types.go":   3,
				"internal/doctor/ratchet_test.go":     1,
				"types/conformance_test.go":           9,
				"internal/doctor/mocks_test.go":       2,
				"internal/job/a.go":                   1,
				"internal/handler/insight/service.go": 3,
			}
		case "cmd/magus/job.go#Fork":
			return map[string]int{"cmd/magus/job.go": 1, "cmd/magus/job_test.go": 2, "types/conformance_test.go": 1}
		}
		return nil
	}

	got := ReferencedOutside(root, row, projects, referrers)

	assert.Equal(t, []ReferencedFile{
		{File: "types/conformance_test.go", Count: 10, Uses: []string{"types/conformance.go", "cmd/magus/job.go#Fork"}},
		{File: "cmd/magus/job_test.go", Count: 2, Uses: []string{"cmd/magus/job.go#Fork"}},
		{File: "internal/doctor/ratchet_test.go", Count: 1, Uses: []string{"types/conformance.go"}},
		{File: "cmd/magus-utils/boundary_types.go", Count: 3, Uses: []string{"types/conformance.go"}},
		{File: "internal/handler/insight/service.go", Count: 3, Uses: []string{"types/conformance.go"}},
	}, got, "tests first, then by count; files the write paths cover and generated outputs are left out")
	assert.Equal(t, map[string]bool{"types/conformance.go#": true, "cmd/magus/job.go#Fork": true, "internal/job/a.go#": true}, asked,
		"a claim asks for its declaration, a directory for each file under it")
	assert.Nil(t, ReferencedOutside(root, types.Job{ReadOnly: true, WritePaths: row.WritePaths}, projects, referrers))
}

func TestRenderReferencedOutsideCapsAndSaysWhatToAdd(t *testing.T) {
	t.Parallel()

	var empty strings.Builder
	RenderReferencedOutside(&empty, nil)
	assert.Empty(t, empty.String(), "nothing is written for none")

	files := []ReferencedFile{{File: "a_test.go", Count: 2, Uses: []string{"a.go"}}}
	for i := range findingsLimit + 1 {
		files = append(files, ReferencedFile{File: fmt.Sprintf("f%02d.go", i), Count: 1, Uses: []string{"a.go#Run"}})
	}
	var out strings.Builder
	RenderReferencedOutside(&out, files)
	got := out.String()

	assert.True(t, strings.HasPrefix(got, referencedHeading+"\n  a_test.go (tests a.go, 2 reference(s))\n  f00.go (uses a.go#Run, 1 reference(s))\n"), got)
	assert.NotContains(t, got, "f11.go")
	assert.Contains(t, got, "and 2 more; -o json lists every one")
	assert.Contains(t, got, "add each one the change touches to the write paths")
}

func TestRenderDeletionCandidatesCapsAndCarriesTheCaveat(t *testing.T) {
	t.Parallel()

	var empty strings.Builder
	RenderDeletionCandidates(&empty, nil)
	assert.Empty(t, empty.String(), "nothing is written for none")

	var many []types.UnreferencedEntry
	for i := range findingsLimit + 3 {
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
