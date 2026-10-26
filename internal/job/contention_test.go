package job

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func TestUnusedWritePaths(t *testing.T) {
	t.Parallel()

	files := map[string]string{".gitattributes": "*.go diff=golang\n", "run.go": claimGo, "a.md": "# A\n", "b.md": "# B\n"}
	root := testkit.NewRepository(t, files).Root()
	holderRepo := testkit.NewRepository(t, files)
	rev := holderRepo.Head()
	holderRepo.WriteTree(map[string]string{"run.go": strings.Replace(claimGo, "a()", "a2()", 1), "a.md": "# A2\n"})
	s := NewStore(tempLocation(t, root))

	holder := func(paths ...string) types.Job {
		return types.Job{ID: "A", State: types.StateRunning, WritePaths: paths, Checkpoint: rev, CheckoutRoot: holderRepo.Root()}
	}

	t.Run("the paths and claims no changed line falls under", func(t *testing.T) {
		t.Parallel()
		h := holder("a.md", "b.md", "run.go#A", "run.go#C", "docs/**", "*.md")
		assert.Equal(t, []string{"b.md", "run.go#C", "docs/**"}, unusedWritePaths(t.Context(), s, []types.Job{h}, h))
	})

	t.Run("every path used is empty, not unknown", func(t *testing.T) {
		t.Parallel()
		h := holder("a.md", "run.go")
		got := unusedWritePaths(t.Context(), s, nil, h)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})

	t.Run("an unreadable footprint is nil", func(t *testing.T) {
		t.Parallel()
		h := holder("a.md")
		h.Checkpoint = ""
		assert.Nil(t, unusedWritePaths(t.Context(), s, []types.Job{h}, h))
		assert.Contains(t, describeUnused("A", nil), "could not be read")
	})
}

func changedRegion(file, decl, driver string) types.RegionChange {
	return types.RegionChange{File: types.FileChange{Path: file}, Declaration: decl, Driver: driver}
}

func TestRegionIndexUses(t *testing.T) {
	t.Parallel()

	regions := []types.RegionChange{
		changedRegion("internal/job/checkout.go", "HardenWritten", "go"),
		changedRegion("internal/job/checkout.go", "", "go"),
		changedRegion("internal/job-x/a.go", "", ""),
		changedRegion("docs/guides/jobs.md", "", ""),
		changedRegion("./README.md", "", ""),
		changedRegion("weird/[a].go", "", ""),
	}
	entries := []string{
		".", "internal", "internal/job", "internal/job/", "internal/job/checkout.go", "internal/jo",
		"internal/job-x", "internal/**", "internal/job/*_test.go", "internal/**/*.go", "docs/**",
		"*.md", "README.md", "{docs,src}/**", "internal/job/checkout.go#HardenWritten",
		"internal/job/checkout.go#WriteProof", "internal/job/checkout.go#" + types.Preamble,
		"internal/job-x/a.go#Anything", "weird/[a].go", "weird", "", "#X",
	}
	used := indexRegions(regions)
	var got []string
	for _, entry := range entries {
		linear := slices.ContainsFunc(regions, func(r types.RegionChange) bool { return usesEntry(entry, r.Location()) })
		assert.Equal(t, linear, used.uses(entry), "entry %q", entry)
		if used.uses(entry) {
			got = append(got, entry)
		}
	}
	assert.Equal(t, []string{
		".", "internal", "internal/job", "internal/job/", "internal/job/checkout.go", "internal/job-x",
		"internal/**", "internal/**/*.go", "docs/**", "*.md", "README.md", "{docs,src}/**",
		"internal/job/checkout.go#HardenWritten", "internal/job/checkout.go#" + types.Preamble,
		"internal/job-x/a.go#Anything", "weird/[a].go", "weird",
	}, got)
}

// BenchmarkRegionIndexUses is an integration row's soft paths against its merge diff, the
// shape that ran a guard's write grade past its budget when every entry walked every region.
func BenchmarkRegionIndexUses(b *testing.B) {
	regions := make([]types.RegionChange, 0, 4000)
	entries := make([]string, 0, 526)
	for i := range 4000 {
		regions = append(regions, changedRegion(fmt.Sprintf("internal/pkg%d/file%d.go", i%200, i), "", ""))
	}
	for i := range 526 {
		entries = append(entries, fmt.Sprintf("internal/pkg%d/file%d_test.go", i%250, i))
	}
	for b.Loop() {
		used := indexRegions(regions)
		for _, entry := range entries {
			used.uses(entry)
		}
	}
}
