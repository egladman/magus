package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

func TestMemoryCurveRoundTrips(t *testing.T) {
	t.Parallel()
	var c MemoryCurve
	for i := range c {
		c[i] = uint32(i * 100)
	}
	raw, err := json.Marshal(&c)
	require.NoError(t, err)
	assert.JSONEq(t, `[0,100,200,300,400,500,600,700,800,900,1000,1100,1200,1300,1400,1500,1600,1700,1800,1900]`, string(raw))

	var back MemoryCurve
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, c, back)
	assert.Equal(t, 1900, back.PeakMB())
}

func TestMemoryProfileRoundTrips(t *testing.T) {
	t.Parallel()
	p := MemoryProfile{D10Ms: 40_000, D90Ms: 400_000, Runs: 3}
	p.Curve[0], p.Curve[19] = 512, 5800
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	assert.JSONEq(t, `{"curve":[512,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,5800],"d10_ms":40000,"d90_ms":400000,"runs":3}`, string(raw))

	var back MemoryProfile
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, p, back)
}

func TestMemoryProfileHeldMB(t *testing.T) {
	t.Parallel()
	ramp := MemoryProfile{D10Ms: 50_000, D90Ms: 100_000, Runs: 3}
	for i := range ramp.Curve {
		ramp.Curve[i] = uint32(100 * (i + 1))
	}
	ramp.Curve[3] = 5000
	cases := []struct {
		name    string
		profile MemoryProfile
		elapsed time.Duration
		want    int
	}{
		{"the start holds the first bucket", ramp, 0, 100},
		{"buckets 2 to 4 of the band, spike included", ramp, 10 * time.Second, 5000},
		{"buckets 5 to 10, past the spike", ramp, 25 * time.Second, 1100},
		{"past D10 the band reaches the last bucket", ramp, 60 * time.Second, 2000},
		{"at D90 the last bucket", ramp, 100 * time.Second, 2000},
		{"past D90 the peak", ramp, 100*time.Second + 1, 5000},
		{"no duration band holds the peak", MemoryProfile{Curve: ramp.Curve, Runs: 3}, time.Second, 5000},
		{"no profile holds nothing", MemoryProfile{}, time.Second, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.profile.HeldMB(tc.elapsed))
		})
	}
}

func TestMemoryCurveRefusesWrongLength(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`[1,2,3]`, `[]`, `[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]`} {
		var c MemoryCurve
		err := json.Unmarshal([]byte(raw), &c)
		require.Error(t, err, raw)
		assert.Contains(t, err.Error(), "buckets, want 20", raw)
	}
	var c MemoryCurve
	err := json.Unmarshal([]byte(`[1,2,3]`), &c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has 3 buckets")
}
