package doctor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/types"
)

// durationHistoryWith writes a history recording one run duration per
// "<spell>/<target>" key and returns its path.
func durationHistoryWith(t *testing.T, project string, runs map[string]time.Duration) string {
	t.Helper()
	h := forecast.History{
		Version:  forecast.HistoryVersion,
		Projects: map[string]map[string]forecast.Stats{project: {}},
	}
	for key, d := range runs {
		h.Projects[project][key] = forecast.Stats{
			Samples:        1,
			RecentOutcomes: []forecast.Outcome{{Result: "pass", DurationMs: d.Milliseconds()}},
		}
	}
	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, h.Save(context.Background(), path))
	return path
}

// The direction that costs a build: the target has grown into its ceiling, so the
// next slow machine fails a run that was correct.
func TestTimeoutDeclarationsReportsACrowdedCeiling(t *testing.T) {
	path := durationHistoryWith(t, ".", map[string]time.Duration{"magusfile/security": 13 * time.Minute})
	got := runnerFor(path).checkTimeoutDeclarations([]*types.Project{
		projectWith(".", map[string]types.Target{"security": {Timeout: "15m"}}),
	})

	assert.Equal(t, types.DoctorAdvice, got.Status)
	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "declares a 15m timeout and has already run for 13m0s")
}

// The other direction: a ceiling so far above every run on record that a hang would
// hold its locks most of a day before anything fired. The number is not wrong, it has
// stopped doing the job it was written for.
func TestTimeoutDeclarationsReportsALooseCeiling(t *testing.T) {
	path := durationHistoryWith(t, ".", map[string]time.Duration{"magusfile/security": 5 * time.Second})
	got := runnerFor(path).checkTimeoutDeclarations([]*types.Project{
		projectWith(".", map[string]types.Target{"security": {Timeout: "12h"}}),
	})

	assert.Equal(t, types.DoctorAdvice, got.Status)
	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "has never run longer than 5s")
}

// Noise control, and the case that matters most: a CORRECTLY written guard sits well
// above its measurements and must draw silence. These are this repository's own two
// declarations, at 7x and 20x their worst recorded run; a threshold that reports
// either one is a threshold that teaches its reader to ignore the check.
func TestTimeoutDeclarationsStaysQuiet(t *testing.T) {
	for _, tc := range []struct {
		name     string
		longest  time.Duration
		declared string
	}{
		{"the repo's ci ceiling at 7x its worst run", 6*time.Minute + 8*time.Second, "45m"},
		{"the repo's security ceiling at 20x its worst run", 44 * time.Second, "15m"},
		{"a target too short to reason about", 500 * time.Millisecond, "10m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := durationHistoryWith(t, ".", map[string]time.Duration{"magusfile/gate": tc.longest})
			got := runnerFor(path).checkTimeoutDeclarations([]*types.Project{
				projectWith(".", map[string]types.Target{"gate": {Timeout: tc.declared}}),
			})
			assert.Equal(t, types.DoctorOK, got.Status)
			assert.Empty(t, got.Details)
		})
	}
}

// An undeclared target is unbounded on purpose, so there is no declaration to keep
// honest and no finding to make - however long it has run.
func TestTimeoutDeclarationsIgnoresUndeclaredTargets(t *testing.T) {
	path := durationHistoryWith(t, ".", map[string]time.Duration{"go/go-test": 15 * time.Minute})
	got := runnerFor(path).checkTimeoutDeclarations([]*types.Project{
		projectWith(".", map[string]types.Target{"go-test": {}}),
	})

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Contains(t, got.Message, "every target is unbounded")
}

// A ceiling has to cover the SLOWEST spell serving the target, so the longest run
// across the histories is what it is measured against.
func TestTimeoutDeclarationsTakesTheLongestRunAcrossSpells(t *testing.T) {
	path := durationHistoryWith(t, ".", map[string]time.Duration{
		"go/test":        30 * time.Second,
		"magusfile/test": 9 * time.Minute,
	})
	got := runnerFor(path).checkTimeoutDeclarations([]*types.Project{
		projectWith(".", map[string]types.Target{"test": {Timeout: "10m"}}),
	})

	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "has already run for 9m0s")
}

// A declared target that has never run draws silence rather than a guess. Nothing was
// measured, so nothing can be said about the declaration.
func TestTimeoutDeclarationsSaysNothingWithoutAMeasurement(t *testing.T) {
	got := runnerFor(filepath.Join(t.TempDir(), "absent.json")).
		checkTimeoutDeclarations([]*types.Project{
			projectWith(".", map[string]types.Target{"security": {Timeout: "15m"}}),
		})

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Empty(t, got.Details)
}
