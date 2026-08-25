package doctor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

const mb = int64(1) << 20

// historyWith writes a history file recording one peak per "<spell>/<target>" key
// and returns its path.
func historyWith(t *testing.T, project string, peaks map[string]int64) string {
	t.Helper()
	h := forecast.History{
		Version:  forecast.HistoryVersion,
		Projects: map[string]map[string]forecast.Stats{project: {}},
	}
	for key, bytes := range peaks {
		h.Projects[project][key] = forecast.Stats{
			Samples:        1,
			RecentOutcomes: []forecast.Outcome{{Result: "pass", MaxRSSBytes: bytes}},
		}
	}
	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, h.Save(context.Background(), path))
	return path
}

func projectWith(path string, policies map[string]types.Target) *types.Project {
	return &types.Project{Path: path, Name: path, TargetPolicies: policies}
}

func runnerFor(historyPath string) *runner {
	return &runner{opts: options{cfg: config.Config{HistoryPath: historyPath}}}
}

// The dangerous direction: the gate keeps admitting the target against a figure it
// has outgrown, and nothing else would ever say so.
func TestMemoryDeclarationsReportsUnderDeclaration(t *testing.T) {
	path := historyWith(t, ".", map[string]int64{"go/test": 9000 * mb})
	got := runnerFor(path).checkMemoryDeclarations([]*types.Project{
		projectWith(".", map[string]types.Target{"test": {MemoryMB: 2048}}),
	})

	assert.Equal(t, types.DoctorAdvice, got.Status)
	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "declares 2048MB and reached at least 9000MB")
}

// The symmetric finding is NOT reported, and this pins that. The recorded figure
// is a floor, so it can argue that a declaration is too small but never that one
// is too large: `test` in this repo declares 10240MB from a real 16GB runner
// death, and the over-declared arm told its author to lower it.
func TestMemoryDeclarationsNeverReportsOverDeclaration(t *testing.T) {
	path := historyWith(t, ".", map[string]int64{"go/test": 3980 * mb})
	got := runnerFor(path).checkMemoryDeclarations([]*types.Project{
		projectWith(".", map[string]types.Target{"test": {MemoryMB: 10240}}),
	})

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Empty(t, got.Details)
}

// The arm that grows the declared set: an undeclared target counts as zero against
// its peers, which is exactly the blind spot machine-wide admission cannot cover.
func TestMemoryDeclarationsReportsAHeavyUndeclaredTarget(t *testing.T) {
	path := historyWith(t, "console", map[string]int64{"typescript/test": 5312 * mb})
	got := runnerFor(path).checkMemoryDeclarations([]*types.Project{
		projectWith("console", nil),
	})

	assert.Equal(t, types.DoctorAdvice, got.Status)
	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "declares no memory_mb anywhere in what it runs and reached at least 5312MB")
}

// Noise control. A report that fires on every small target is one nobody reads, and
// these three are the shapes that would fire most often.
func TestMemoryDeclarationsStaysQuiet(t *testing.T) {
	for _, tc := range []struct {
		name     string
		peak     int64
		declared int
	}{
		{"a small undeclared target is below the floor", 300 * mb, 0},
		{"a peak just over the declaration is not drift", 2200 * mb, 2048},
		// A declaration far above every recorded peak draws silence on purpose.
		// The recorded peak is a floor (types.PeakRSS folds a target's processes
		// as a maximum), so it cannot support "you declared too much" - the
		// finding this repo's own `test` target would have received wrongly.
		{"a declaration well above the recorded peak is not reported", 3000 * mb, 10240},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := historyWith(t, ".", map[string]int64{"go/test": tc.peak})
			got := runnerFor(path).checkMemoryDeclarations([]*types.Project{
				projectWith(".", map[string]types.Target{"test": {MemoryMB: tc.declared}}),
			})
			assert.Equal(t, types.DoctorOK, got.Status)
			assert.Empty(t, got.Details)
		})
	}
}

// One target served by two spells has two histories, and the figure that has to fit
// on a machine is the larger of them.
func TestMemoryDeclarationsTakesTheLargestPeakAcrossSpells(t *testing.T) {
	path := historyWith(t, ".", map[string]int64{
		"go/test":        1000 * mb,
		"magusfile/test": 9000 * mb,
	})
	got := runnerFor(path).checkMemoryDeclarations([]*types.Project{
		projectWith(".", map[string]types.Target{"test": {MemoryMB: 2048}}),
	})

	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "reached at least 9000MB")
}

// A fresh clone has no history, and "everything agrees" would claim a comparison
// that never happened.
func TestMemoryDeclarationsSaysWhenNothingHasRun(t *testing.T) {
	got := runnerFor(filepath.Join(t.TempDir(), "absent.json")).
		checkMemoryDeclarations([]*types.Project{projectWith(".", nil)})

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Contains(t, got.Message, "nothing to compare")
}
