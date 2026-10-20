package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var costEpoch = time.Date(2026, time.October, 13, 9, 0, 0, 0, time.UTC)

// fixtureModels stand in for the rows a harness spell publishes; no table is built into
// magus.
func fixtureModels() model.Table {
	return model.InEffect([]types.ModelPricing{
		{ID: "claude-fable-5-1", Window: 1_000_000, Input: 10, CacheWrite5m: 12.50, CacheWrite1h: 20, CacheRead: 0.25, Output: 50},
		{ID: "claude-opus-5-5", Window: 1_000_000, Input: 4, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.20, Output: 20},
		{ID: "claude-sonnet-5-5", Window: 1_000_000, Input: 2, CacheWrite5m: 2.50, CacheWrite1h: 4, CacheRead: 0.10, Output: 10},
		{ID: "claude-haiku-5-5", Window: 1_000_000, Input: 0.10, CacheWrite5m: 0.125, CacheWrite1h: 0.20, CacheRead: 0.01, Output: 0.50,
			LongContext: &types.PriceStep{Above: 100_000, Multiplier: 5}},
		{ID: "claude-haiku-4-5", Window: 200_000, Input: 1, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.10, Output: 5},
	}, nil)
}

func at(sec int) time.Time { return costEpoch.Add(time.Duration(sec) * time.Second) }

func mainCall(sec int, size int64, mdl string) trail.SessionCall {
	return trail.SessionCall{At: at(sec), AgentFacts: types.AgentFacts{Thread: types.AgentThreadMain, ContextTokens: size, ContextModel: mdl}}
}

func TestSummarizeCostMeasuresEverySectionFromACompleteTrail(t *testing.T) {
	tr := &trail.SessionTrail{Commands: 6, Spawns: []trail.SessionSpawn{
		{Child: "Explore", At: at(5), DeclaredModel: "claude-sonnet-5-5"},
		{Child: "Explore", At: at(100), DeclaredModel: ""},
	}}
	var calls []trail.SessionCall
	for sec := 1; sec <= 30; sec++ {
		c := mainCall(sec, 300_000, "claude-opus-5-5")
		c.Billing = types.AgentBillingMetered
		c.ResultChars = new(1000)
		c.Shadow = map[types.GuardProfile]types.GuardDecision{
			types.GuardProfileStrict: types.GuardDeny,
			types.GuardProfileLight:  types.GuardAllow,
		}
		switch sec {
		case 1:
			c.ContextTokens = 100_000
			c.Decision, c.Rule, c.DelegableClass = "advise", "root-result-cap", types.DelegableSearch // answered by the spawn at 5s
		case 15:
			c.Decision, c.Rule, c.DelegableClass = "advise", "root-repeat", types.DelegableDiff // the next spawn is past the window
		case 16:
			c.Decision, c.Rule = "advise", "spawn-names-model" // not a cost advisory
		case 30:
			c.ContextTokens = 600_000
		}
		calls = append(calls, c)
	}
	calls = append(calls,
		trail.SessionCall{At: at(2), AgentFacts: types.AgentFacts{Thread: types.AgentThreadSub, ContextTokens: 900_000, ContextModel: "claude-haiku-5-5"}},
		trail.SessionCall{At: at(3), AgentFacts: types.AgentFacts{Thread: types.AgentThreadUnknown, ContextTokens: 50_000}})

	tr.Calls = calls

	got := summarizeCost(tr, fixtureModels())

	require.NotNil(t, got.Dollars)
	assert.Equal(t, sessionCost{
		RootContext: &rootContext{Peak: 600_000, P50: 300_000, Samples: 30, Models: []string{"claude-opus-5-5"}},
		Delegable: &delegableShare{
			Chars: 2000, Total: 30_000,
			ByClass: []countedName{{Name: "diff", Count: 1000}, {Name: "search", Count: 1000}},
		},
		// An undeclared model is stored as itself; the renderer labels it.
		Spawns:     &spawnTally{Total: 2, ByModel: []countedName{{Name: "", Count: 1}, {Name: "claude-sonnet-5-5", Count: 1}}},
		Advisories: &advisoryOutcome{Given: 2, Followed: 1, Within: followWithin},
		Shadow: []shadowTally{
			{Profile: types.GuardProfileLight, Decisions: map[types.GuardDecision]int{types.GuardAllow: 30}},
			{Profile: types.GuardProfileStrict, Decisions: map[types.GuardDecision]int{types.GuardDeny: 30}},
		},
		Dollars: got.Dollars, // pinned below
	}, *got, "every section was measured")
	assert.InDelta(t, 2000.0/30_000.0, got.Delegable.Share(), 1e-12)
	assert.Equal(t, 1, got.Advisories.Ignored())
	assert.Equal(t, 3, got.Dollars.Calls, "100k, the run of 300k, and 600k")
	assert.InDelta(t, (0.1+0.3+0.6)*0.20, got.Dollars.FloorUSD, 1e-9)
}

func TestSummarizeCostSaysNothingWasMeasuredWithoutATrail(t *testing.T) {
	got := summarizeCost(nil, fixtureModels())

	assert.Nil(t, got.RootContext)
	assert.Nil(t, got.Dollars)
	assert.Len(t, got.NotMeasured, len(costSections), "every section names why it is empty")
}

func TestSummarizeCostCountsSpawnsFromATrailWithNoPerCallFacts(t *testing.T) {
	tr := &trail.SessionTrail{Commands: 3, Spawns: []trail.SessionSpawn{{Child: "Explore", At: at(1), DeclaredModel: "claude-sonnet-5-5"}}}

	got := summarizeCost(tr, fixtureModels())

	assert.Equal(t, &spawnTally{Total: 1, ByModel: []countedName{{Name: "claude-sonnet-5-5", Count: 1}}}, got.Spawns)
	assert.Nil(t, got.RootContext)
	assert.Equal(t, noCallFacts, got.NotMeasured[costRootContext])
	assert.Equal(t, noCallFacts, got.NotMeasured[costAdvisories])
	assert.NotContains(t, got.NotMeasured, costSpawns)
}

// The path `session show` takes: calls written by the hook, folded by ForSession, summarized.
func TestSummarizeCostReadsTheFactsAHookRecorded(t *testing.T) {
	base := t.TempDir()
	for range 2 {
		trail.AppendAgentCommand(t.Context(), base, trail.AgentCommand{
			Session: "s1", Tool: "shell.command", Command: "grep -r x .", Decision: "pass",
			AgentFacts: types.AgentFacts{
				Thread: types.AgentThreadMain, ThreadFrom: types.ThreadReported, ContextTokens: 250_000, ContextModel: "claude-opus-5-5",
				Billing: types.AgentBillingMetered, DelegableClass: types.DelegableSearch, ResultChars: new(4000),
				Shadow: map[types.GuardProfile]types.GuardDecision{types.GuardProfileStrict: types.GuardDeny},
			},
		})
	}
	tr := trail.ForSession(base, "s1", 100)

	got := summarizeCost(&tr, fixtureModels())

	// The other sections have their own tests.
	want := *got
	want.RootContext = &rootContext{Peak: 250_000, P50: 250_000, Samples: 2, Models: []string{"claude-opus-5-5"}}
	want.Shadow = []shadowTally{{Profile: types.GuardProfileStrict, Decisions: map[types.GuardDecision]int{types.GuardDeny: 2}}}
	assert.Equal(t, want, *got)
	assert.Equal(t, chars(8000), got.Delegable.Chars)
	require.NotNil(t, got.Dollars)
	assert.Equal(t, types.AgentBillingMetered, got.Dollars.Billing)
}

func TestSummarizeCostNeverCountsAnUnknownThreadAsTheRoot(t *testing.T) {
	calls := []trail.SessionCall{{At: at(1), AgentFacts: types.AgentFacts{Thread: types.AgentThreadUnknown, ContextTokens: 700_000, ContextModel: "claude-opus-5-5"}}}

	got := summarizeCost(&trail.SessionTrail{Commands: 1, Calls: calls}, fixtureModels())

	assert.Nil(t, got.RootContext)
	assert.Equal(t, "no call is known to be on the main thread (1 of unknown thread)", got.NotMeasured[costRootContext])
}

func TestSummarizeCostLeavesItsCallersCallsInOrder(t *testing.T) {
	calls := []trail.SessionCall{mainCall(2, 1, "m"), mainCall(1, 1, "m")}

	summarizeCost(&trail.SessionTrail{Calls: calls}, fixtureModels())

	assert.Equal(t, []time.Time{at(2), at(1)}, []time.Time{calls[0].At, calls[1].At})
}

// No call observing a result is unmeasured; a call observing an empty one is a measured
// zero.
func TestDelegableTellsAnUnobservedResultFromAnEmptyOne(t *testing.T) {
	assert.Nil(t, sumDelegable([]trail.SessionCall{mainCall(1, 1, "m")}))

	empty := mainCall(1, 1, "m")
	empty.ResultChars = new(0)
	got := sumDelegable([]trail.SessionCall{empty})
	require.NotNil(t, got)
	assert.Zero(t, got.Total)
	assert.Zero(t, got.Share())
}

func TestDollarsPriceTheCacheReadFloorOnceForEachModelCall(t *testing.T) {
	metered := func(sec int, size int64, mdl string) trail.SessionCall {
		c := mainCall(sec, size, mdl)
		c.Billing = types.AgentBillingMetered
		return c
	}
	calls := []trail.SessionCall{
		metered(1, 500_000, "claude-opus-5-5"),
		metered(2, 500_000, "claude-opus-5-5"), // a second tool of the same model call
		metered(3, 600_000, "claude-opus-5-5"),
		metered(4, 150_000, "claude-haiku-5-5"), // past its 100k step: 5x the cache-read price
	}

	est, why := estimateDollars(calls, fixtureModels())

	require.NotNil(t, est, why)
	assert.Equal(t, 3, est.Calls)
	assert.InDelta(t, 0.5*0.20+0.6*0.20+0.15*0.01*5, est.FloorUSD, 1e-9)
}

func TestDollarsAreWithheldWhenTheyWouldBeAGuess(t *testing.T) {
	for name, tc := range map[string]struct {
		billing types.AgentBilling
		model   string
		want    string
	}{
		"quota":         {types.AgentBillingQuota, "claude-opus-5-5", "billing is quota, which has no per-token price"},
		"unset":         {"", "claude-opus-5-5", "billing is unknown: set agents.billing in magus.yaml"},
		"unknown model": {types.AgentBillingMetered, "claude-opus-9", `model "claude-opus-9" is not in the model table: add it under agents.models in magus.yaml`},
	} {
		t.Run(name, func(t *testing.T) {
			c := mainCall(1, 400_000, tc.model)
			c.Billing = tc.billing

			est, why := estimateDollars([]trail.SessionCall{c}, fixtureModels())

			assert.Nil(t, est)
			assert.Equal(t, tc.want, why)
		})
	}
}

func TestDollarsAreWithheldWhenOneCallLacksBilling(t *testing.T) {
	billed := mainCall(1, 400_000, "claude-opus-5-5")
	billed.Billing = types.AgentBillingMetered

	est, why := estimateDollars([]trail.SessionCall{billed, mainCall(2, 410_000, "claude-opus-5-5")}, fixtureModels())

	assert.Nil(t, est)
	assert.Contains(t, why, "billing is unknown")
}

func TestAdvisoryFollowEndsAfterTheWindow(t *testing.T) {
	adv := mainCall(0, 1, "m")
	adv.Decision, adv.Rule = "advise", "root-heavy-command"
	main := []trail.SessionCall{adv}
	for i := 1; i <= followWithin+2; i++ {
		main = append(main, mainCall(i, 1, "m"))
	}

	inside := tallyAdvisories(main, []costSpawn{{At: at(followWithin)}})
	outside := tallyAdvisories(main, []costSpawn{{At: at(followWithin + 1)}})

	assert.Equal(t, 1, inside.Followed)
	assert.Equal(t, 0, outside.Followed)
	assert.Equal(t, 1, outside.Ignored())
}

// A decision outside allow, advise and deny is counted under its own name, never folded
// into another.
func TestShadowTallyPrintsEveryDecision(t *testing.T) {
	tally := shadowTally{Profile: types.GuardProfileStandard, Decisions: map[types.GuardDecision]int{types.GuardAdvise: 2, "ask": 1}}

	assert.Equal(t, "standard 0 allow/2 advise/0 deny/1 ask", tally.String())
}

func TestRenderCostPrintsMeasuredAndUnmeasuredSections(t *testing.T) {
	tr := &trail.SessionTrail{Commands: 2, Spawns: []trail.SessionSpawn{
		{Child: "Explore", At: at(9), DeclaredModel: "claude-sonnet-5-5"},
		{Child: "Explore", At: at(10)},
	}}
	c1 := mainCall(1, 612_000, "claude-opus-5-5")
	c1.Billing = types.AgentBillingMetered
	c1.ResultChars = new(1500)
	c1.DelegableClass = types.DelegableSearch
	tr.Calls = []trail.SessionCall{c1}
	got := summarizeCost(tr, fixtureModels())

	var buf bytes.Buffer
	renderCost(&buf, got)

	assert.Equal(t, `
Cost, with the root as the main thread:
  root context     peak 612k, p50 612k over 1 call(s), claude-opus-5-5
  delegable        100% of 1k result chars (search 1k)
  spawns           2: none declared 1, claude-sonnet-5-5 1
  advisories       0 given, 0 followed by a spawn within 10 calls, 0 ignored
  shadow           not measured: no call recorded what each profile would have decided
  dollars          $0.12 floor: cache reads of the root's 1 model call(s), metered
`, buf.String())
}

func TestSessionShowNamesTheCostSectionsItCouldNotMeasure(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := t.TempDir()
	_, err := loadStream(t, root, `{"host":"h1","session":"s1","ts":1,"kind":"shell.command","ref":"r1","text":"ls"}`)
	require.NoError(t, err)

	out := captureStdout(t, func() { require.NoError(t, sessionShow(root, []string{"s1"})) })

	assert.Contains(t, out, "Cost, with the root as the main thread:")
	assert.Contains(t, out, "root context     not measured: this checkout's trail holds nothing for the session")
	assert.NotContains(t, out, "$0.00")
}

func TestHarnessModelRowsOpenTheWorkspaceOnceAndOnlyWhenAsked(t *testing.T) {
	opened := 0
	rows := harnessModelRows(t.Context(), t.TempDir(), func(context.Context) (types.WorkspaceRepository, error) {
		opened++
		return nil, errors.New("magusfile.buzz does not load")
	})

	assert.Zero(t, opened, "building the reader reads nothing")
	assert.Empty(t, rows(), "a workspace that will not load has no rows")
	assert.Empty(t, rows())
	assert.Equal(t, 1, opened)
}
