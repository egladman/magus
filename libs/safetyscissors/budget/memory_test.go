package budget

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MemorySuite struct{ suite.Suite }

func TestMemory(t *testing.T) { suite.Run(t, new(MemorySuite)) }

func (s *MemorySuite) TestCurveRoundTrips() {
	t := s.T()
	var c Curve
	for i := range c {
		c[i] = uint32(i * 100)
	}
	raw, err := json.Marshal(&c)
	require.NoError(t, err)
	assert.JSONEq(t, `[0,100,200,300,400,500,600,700,800,900,1000,1100,1200,1300,1400,1500,1600,1700,1800,1900]`, string(raw))

	var back Curve
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, c, back)
	assert.Equal(t, 1900, back.PeakMB())
}

func (s *MemorySuite) TestProfileRoundTrips() {
	t := s.T()
	p := Profile{D10Ms: 40_000, D90Ms: 400_000, Runs: 3}
	p.Curve[0], p.Curve[19] = 512, 5800
	raw, err := json.Marshal(&p)
	require.NoError(t, err)
	assert.JSONEq(t, `{"curve":[512,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,5800],"d10_ms":40000,"d90_ms":400000,"runs":3}`, string(raw))

	var back Profile
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, p, back)
}

// ramp is a profile whose buckets climb by 100 MiB, with a spike in bucket 3.
func ramp() Profile {
	p := Profile{D10Ms: 50_000, D90Ms: 100_000, Runs: 3}
	for i := range p.Curve {
		p.Curve[i] = uint32(100 * (i + 1))
	}
	p.Curve[3] = 5000
	return p
}

func (s *MemorySuite) TestHeldMBAtTheStartIsTheFirstBucket() {
	assert.Equal(s.T(), 100, ramp().HeldMB(0))
}

func (s *MemorySuite) TestHeldMBSpansTheBandsBucketsSpikeIncluded() {
	assert.Equal(s.T(), 5000, ramp().HeldMB(10*time.Second), "buckets 2 to 4")
	assert.Equal(s.T(), 1100, ramp().HeldMB(25*time.Second), "buckets 5 to 10, past the spike")
}

func (s *MemorySuite) TestHeldMBPastD10ReachesTheLastBucket() {
	assert.Equal(s.T(), 2000, ramp().HeldMB(60*time.Second))
	assert.Equal(s.T(), 2000, ramp().HeldMB(100*time.Second), "at D90")
}

func (s *MemorySuite) TestHeldMBPastD90IsThePeak() {
	assert.Equal(s.T(), 5000, ramp().HeldMB(100*time.Second+1))
}

func (s *MemorySuite) TestHeldMBWithoutABandIsThePeak() {
	assert.Equal(s.T(), 5000, Profile{Curve: ramp().Curve, Runs: 3}.HeldMB(time.Second))
	assert.Equal(s.T(), 0, Profile{}.HeldMB(time.Second), "no profile holds nothing")
}

func (s *MemorySuite) TestCurveRefusesWrongLength() {
	t := s.T()
	for _, raw := range []string{`[1,2,3]`, `[]`, `[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]`} {
		var c Curve
		err := json.Unmarshal([]byte(raw), &c)
		require.Error(t, err, raw)
		assert.Contains(t, err.Error(), "buckets, want 20", raw)
	}
	var c Curve
	err := json.Unmarshal([]byte(`[1,2,3]`), &c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has 3 buckets")
}

func (s *MemorySuite) TestSizingString() {
	t := s.T()
	assert.Equal(t, "declared", Sizing{}.String())
	assert.Equal(t, "measured over 54 runs", Sizing{Samples: 54}.String())
	assert.Equal(t, "measured over 9 runs of any shape", Sizing{Samples: 9, AnyShape: true}.String())
	assert.Equal(t, "declared; no memory curve", Sizing{Fallback: "no memory curve"}.String())
}

func (s *MemorySuite) TestSlotSizingString() {
	t := s.T()
	assert.Empty(t, SlotSizing{}.String(), "nothing recorded says nothing")
	assert.Equal(t, "declared", SlotSizing{Source: SlotsDeclared}.String())
	assert.Equal(t, "unmeasured", SlotSizing{Source: SlotsUnmeasured}.String())
	assert.Equal(t, "for its memory", SlotSizing{Source: SlotsForMemory}.String())
	assert.Equal(t, "measured over 4 runs", SlotSizing{Source: SlotsMeasured, Samples: 4}.String())
	assert.Equal(t, "measured over 7 runs of any shape", SlotSizing{Source: SlotsMeasured, Samples: 7, AnyShape: true}.String())
}

func (s *MemorySuite) TestFormatMB() {
	t := s.T()
	assert.Equal(t, "512 MiB", FormatMB(512))
	assert.Equal(t, "1.0 GiB", FormatMB(1024))
	assert.Equal(t, "10.0 GiB", FormatMB(10240))
}

func (s *MemorySuite) TestSizingBreakdown() {
	t := s.T()
	assert.Equal(t, "lint 4.8 GiB: own peak over 48 runs", Sizing{Samples: 48}.Breakdown("lint", "", 4915))
	assert.Equal(t, "conventions 4.2 GiB under lint: own peak over 12 runs", Sizing{Samples: 12}.Breakdown("conventions", "lint", 4300))
	assert.Equal(t, "lint 512 MiB: declared", Sizing{}.Breakdown("lint", "", 512))
	assert.Equal(t, "lint 1.0 GiB: own peak over 3 runs of any shape", Sizing{Samples: 3, AnyShape: true}.Breakdown("lint", "", 1024))
	assert.Equal(t, "lint 1.0 GiB: declared; no memory curve; held flat", Sizing{Fallback: "no memory curve; held flat"}.Breakdown("lint", "", 1024))
}
