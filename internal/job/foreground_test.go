package job

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestCheckOverrun(t *testing.T) {
	row := types.Job{ID: "w", Check: &types.LeaseCheck{Target: "go-test", Project: "."}}
	took := func(d time.Duration) func(string, string) (time.Duration, bool) {
		return func(project, target string) (time.Duration, bool) {
			assert.Equal(t, ".", project)
			assert.Equal(t, "go-test", target)
			return d, d > 0
		}
	}

	limit := func(d time.Duration) func() (time.Duration, bool) {
		return func() (time.Duration, bool) { return d, d > 0 }
	}

	o := CheckOverrun(row, took(14*time.Minute), limit(10*time.Minute))
	require.NotNil(t, o)
	assert.Equal(t, ForegroundOverrun{Check: row.Check.String(), LastRunMs: 840000, ForegroundMaxMs: 600000}, *o)
	assert.Contains(t, o.Check, "run go-test .")

	var out bytes.Buffer
	RenderForegroundOverrun(&out, o)
	assert.Contains(t, out.String(), "last ran 14m0s here, past the 10m0s a foreground call may wait")
	assert.Contains(t, out.String(), "fork a narrower check")

	assert.Nil(t, CheckOverrun(row, took(5*time.Minute), limit(10*time.Minute)), "a check that fits")
	assert.Nil(t, CheckOverrun(row, took(14*time.Minute), limit(0)), "no host declares a maximum")
	assert.Nil(t, CheckOverrun(types.Job{ID: "r", ReadOnly: true}, took(14*time.Minute), limit(10*time.Minute)), "no check")
	assert.Nil(t, CheckOverrun(row, took(0), func() (time.Duration, bool) {
		t.Error("the harnesses are not loaded for a check with no recorded run")
		return 0, false
	}), "no recorded run")

	out.Reset()
	RenderForegroundOverrun(&out, nil)
	assert.Empty(t, out.String())
}
