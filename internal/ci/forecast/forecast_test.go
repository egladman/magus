package forecast

import (
	"testing"

	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type BudgetSuite struct{ suite.Suite }

func TestBudgetSuite(t *testing.T) { suite.Run(t, new(BudgetSuite)) }

func (s *BudgetSuite) TestAMachineBudgetLeavesHeadroom() {
	got := BudgetBytes(16384)
	s.Positive(got)
	s.Less(got, int64(16384)<<20)
	s.Equal(int64(float64(int64(16384)<<20)*mem.UsableFraction), got)
}

type ColdSuite struct{ suite.Suite }

func TestColdSuite(t *testing.T) { suite.Run(t, new(ColdSuite)) }

func (s *ColdSuite) TestNoMeasurementIsCold() {
	s.True(Model{}.Cold(unitList("a:test", "b:lint")))
}

func (s *ColdSuite) TestOneMeasuredUnitIsWarm() {
	h := measured(1, map[string]int64{"a:test": 60_000})
	s.False(Model{History: h}.Cold(unitList("a:test", "b:lint")))
}

func (s *ColdSuite) TestNoUnitsIsNotCold() {
	s.False(Model{}.Cold(nil))
}

type PlanSuite struct{ suite.Suite }

func TestPlanSuite(t *testing.T) { suite.Run(t, new(PlanSuite)) }

// noBudget pins an effectively infinite budget. Zero would read this
// host's memory, so the assertion would differ between machines.
const noBudget = int64(1) << 62

func unit(s string) types.Unit {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return types.Unit{Project: s[:i], Target: s[i+1:]}
		}
	}
	return types.Unit{Project: s}
}

func unitList(names ...string) []types.Unit {
	out := make([]types.Unit, len(names))
	for i, n := range names {
		out[i] = unit(n)
	}
	return out
}

// measured is a history with setupMs and each project:target at its ms.
func measured(setupMs int64, durations map[string]int64) History {
	h := History{Constants: Constants{SetupP50Ms: Millis(setupMs)}, Projects: map[string]map[string]Stats{}}
	for name, ms := range durations {
		u := unit(name)
		if h.Projects[u.Project] == nil {
			h.Projects[u.Project] = map[string]Stats{}
		}
		h.Projects[u.Project][u.Target] = Stats{P75Ms: ms, Samples: 50}
	}
	return h
}

func names(shards [][]types.Unit) [][]string {
	out := make([][]string, len(shards))
	for i, s := range shards {
		for _, u := range s {
			out[i] = append(out[i], u.String())
		}
	}
	return out
}

func closures(edges map[string][]string) func(types.Unit) []types.Unit {
	return func(u types.Unit) []types.Unit {
		return append([]types.Unit{u}, unitList(edges[u.String()]...)...)
	}
}

func seedPeak(h *History, project, target string, bytes int64) {
	s := h.Projects[project][target]
	s.RecentOutcomes = append(s.RecentOutcomes, Outcome{Result: OutcomePass, MaxRSSBytes: bytes})
	h.Projects[project][target] = s
}

func (s *PlanSuite) TestEmptyHistoryPlacesEveryUnitOnce() {
	us := unitList("a:test", "b:test", "c:lint", "d:test", "e:build")
	got := Model{MemoryBudgetBytes: noBudget}.Plan(us, 8)
	seen := map[types.Unit]int{}
	for _, shard := range got {
		for _, u := range shard {
			seen[u]++
		}
	}
	s.Len(seen, len(us))
	for _, u := range us {
		s.Equalf(1, seen[u], "%s placed once", u)
	}
}

func (s *PlanSuite) TestLongUnitsEachGetAShard() {
	h := measured(1_000, map[string]int64{".:test": 100_000, ".:lint": 100_000, ".:build": 100_000})
	got := Model{History: h, MemoryBudgetBytes: noBudget}.Plan(unitList(".:test", ".:lint", ".:build"), 3)
	s.Equal([][]string{{".:build"}, {".:lint"}, {".:test"}}, names(got))
}

func (s *PlanSuite) TestAUnitShorterThanSetupJoinsAShard() {
	h := measured(30_000, map[string]int64{"a:test": 100_000, "b:test": 10_000})
	got := Model{History: h, MemoryBudgetBytes: noBudget}.Plan(unitList("a:test", "b:test"), 8)
	s.Equal([][]string{{"a:test", "b:test"}}, names(got), "a 10s unit does not buy a 30s runner")
}

func (s *PlanSuite) TestASharedPrerequisiteKeepsItsUnitsTogether() {
	h := measured(10_000, map[string]int64{".:build": 200_000, ".:lint": 50_000, ".:test": 50_000})
	f := Model{History: h, MemoryBudgetBytes: noBudget, Needs: closures(map[string][]string{
		".:lint": {".:build"}, ".:test": {".:build"},
	})}
	got := f.Plan(unitList(".:lint", ".:test"), 8)
	s.Equal([][]string{{".:lint", ".:test"}}, names(got), "splitting saves 50s and repeats a 200s build")
}

func (s *PlanSuite) TestDisjointPrerequisitesSplit() {
	h := measured(10_000, map[string]int64{"a:build": 200_000, "b:build": 200_000, "a:test": 50_000, "b:test": 50_000})
	f := Model{History: h, MemoryBudgetBytes: noBudget, Needs: closures(map[string][]string{
		"a:test": {"a:build"}, "b:test": {"b:build"},
	})}
	got := f.Plan(unitList("a:test", "b:test"), 8)
	s.Equal([][]string{{"a:test"}, {"b:test"}}, names(got))
}

func (s *PlanSuite) TestTiesBreakOnProjectTarget() {
	h := measured(1, map[string]int64{"a:test": 60_000, "b:test": 60_000, "c:test": 60_000})
	got := Model{History: h, MemoryBudgetBytes: noBudget}.Plan(unitList("c:test", "b:test", "a:test"), 2)
	s.Equal([][]string{{"a:test", "c:test"}, {"b:test"}}, names(got))
}

func (s *PlanSuite) TestInputOrderDoesNotChangeThePlan() {
	h := measured(5_000, map[string]int64{"a:test": 90_000, "b:lint": 40_000, "c:test": 40_000, "d:build": 20_000})
	f := Model{History: h, MemoryBudgetBytes: noBudget}
	s.Equal(f.Plan(unitList("a:test", "b:lint", "c:test", "d:build"), 4),
		f.Plan(unitList("d:build", "c:test", "b:lint", "a:test"), 4))
}

func (s *PlanSuite) TestMaxShardsCapsTheFanOut() {
	h := measured(1_000, map[string]int64{"a:test": 100_000, "b:test": 100_000, "c:test": 100_000, "d:test": 100_000})
	got := Model{History: h, MemoryBudgetBytes: noBudget}.Plan(unitList("a:test", "b:test", "c:test", "d:test"), 2)
	s.Equal([][]string{{"a:test", "c:test"}, {"b:test", "d:test"}}, names(got))
}

func (s *PlanSuite) TestAMemoryBudgetSplitsAShardThatWouldNotFit() {
	h := measured(600_000, map[string]int64{"docs:ci": 60_000, ".:test": 60_000})
	seedPeak(&h, "docs", "ci", 7<<30)
	seedPeak(&h, ".", "test", 6<<30)
	us := unitList("docs:ci", ".:test")

	s.Len(Model{History: h, MemoryBudgetBytes: noBudget}.Plan(us, 8), 1, "the time model consolidates these")
	s.Len(Model{History: h, MemoryBudgetBytes: 12 << 30}.Plan(us, 8), 2, "13GB exceeds a 12GB budget")
}

func (s *PlanSuite) TestAMemoryBudgetLeavesAFittingPlanAlone() {
	h := measured(600_000, map[string]int64{"a:test": 60_000, "b:test": 60_000})
	seedPeak(&h, "a", "test", 200<<20)
	seedPeak(&h, "b", "test", 300<<20)
	s.Len(Model{History: h, MemoryBudgetBytes: 16 << 30}.Plan(unitList("a:test", "b:test"), 8), 1)
}

func (s *PlanSuite) TestAUnitOverBudgetAloneStopsAtTheCap() {
	h := measured(600_000, map[string]int64{"huge:test": 60_000, "small:test": 60_000})
	seedPeak(&h, "huge", "test", 100<<30)
	seedPeak(&h, "small", "test", 1<<20)
	got := Model{History: h, MemoryBudgetBytes: 1 << 30}.Plan(unitList("huge:test", "small:test"), 8)
	s.Equal([][]string{{"huge:test"}, {"small:test"}}, names(got))
}

func (s *PlanSuite) TestOverBudgetNamesTheShardThatStillDoesNotFit() {
	h := measured(1, map[string]int64{"huge:test": 60_000})
	seedPeak(&h, "huge", "test", 100<<30)
	f := Model{History: h, MemoryBudgetBytes: 1 << 30}
	s.Equal([]int{0}, f.OverBudget(f.Plan(unitList("huge:test"), 8)))
}

func (s *PlanSuite) TestUnmeasuredNamesUnitsAndPrerequisites() {
	h := measured(1, map[string]int64{".:test": 60_000})
	f := Model{History: h, Needs: closures(map[string][]string{".:test": {".:generate"}})}
	s.Equal(unitList(".:generate", "b:lint"), f.Unmeasured(unitList(".:test", "b:lint")))
}

func (s *PlanSuite) TestSufficientShardsCountsTheShardsTheMakespanNeeds() {
	h := measured(1_000, map[string]int64{"a:test": 100_000, "b:test": 50_000, "c:test": 50_000})
	f := Model{History: h}
	us := unitList("a:test", "b:test", "c:test")
	s.Equal(2, f.SufficientShards(us, 3))
	s.Equal(1, f.SufficientShards(us, 1))
}
