package types

import (
	"testing"

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
