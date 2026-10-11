package job

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func lastRunTook(d time.Duration) LastRun {
	return func(string, string) (time.Duration, bool) { return d, d > 0 }
}

func foregroundLimit(d time.Duration) ForegroundMax {
	return func() (time.Duration, bool) { return d, d > 0 }
}

func TestOverrunsForegroundNamesACheckLongerThanTheForeground(t *testing.T) {
	t.Parallel()

	row := types.Job{ID: "w", Check: &types.LeaseCheck{Target: "go-test", Project: "."}}
	asked := func(project, target string) (time.Duration, bool) {
		assert.Equal(t, ".", project)
		assert.Equal(t, "go-test", target)
		return 14 * time.Minute, true
	}

	o, ok := OverrunsForeground(row, asked, foregroundLimit(10*time.Minute))
	require.True(t, ok)
	assert.Equal(t, ForegroundOverrun{Check: row.Check.String(), LastRunMs: 840000, ForegroundMaxMs: 600000}, o)
	assert.Contains(t, o.Check, "run go-test .")

	var out bytes.Buffer
	RenderForegroundOverrun(&out, &o)
	assert.Contains(t, out.String(), "last ran 14m0s here, past the 10m0s a foreground call may wait")
	assert.Contains(t, out.String(), "fork a narrower check")

	out.Reset()
	RenderForegroundOverrun(&out, nil)
	assert.Empty(t, out.String())
}

func TestOverrunsForegroundIsFalseWhenNothingOverruns(t *testing.T) {
	t.Parallel()

	row := types.Job{ID: "w", Check: &types.LeaseCheck{Target: "go-test", Project: "."}}
	for name, tc := range map[string]struct {
		row     types.Job
		lastRun LastRun
		limit   ForegroundMax
	}{
		"a check that fits":          {row, lastRunTook(5 * time.Minute), foregroundLimit(10 * time.Minute)},
		"no host declares a maximum": {row, lastRunTook(14 * time.Minute), foregroundLimit(0)},
		"no check":                   {types.Job{ID: "r", ReadOnly: true}, lastRunTook(14 * time.Minute), foregroundLimit(10 * time.Minute)},
		"no recorded run": {row, lastRunTook(0), func() (time.Duration, bool) {
			t.Error("the harnesses are not loaded for a check with no recorded run")
			return 0, false
		}},
	} {
		_, ok := OverrunsForeground(tc.row, tc.lastRun, tc.limit)
		assert.False(t, ok, name)
	}
}

// A check with no recorded run says nothing about the goals after it.
func TestOverrunsForegroundLooksPastACheckWithNoRecordedRun(t *testing.T) {
	t.Parallel()

	row := types.Job{
		ID:    "w",
		Check: &types.LeaseCheck{Target: "go-vet", Project: "."},
		Goals: []types.Goal{{ID: "test", Kind: types.GoalKindCheck, Check: types.LeaseCheck{Target: "go-test", Project: "."}}},
	}
	asked := 0
	lastRun := func(_, target string) (time.Duration, bool) {
		if target == "go-test" {
			return 14 * time.Minute, true
		}
		return 0, false
	}
	limit := func() (time.Duration, bool) {
		asked++
		return 10 * time.Minute, true
	}

	o, ok := OverrunsForeground(row, lastRun, limit)

	require.True(t, ok)
	assert.Contains(t, o.Check, "run go-test .")
	assert.Equal(t, 1, asked, "the limit is asked once")
}
