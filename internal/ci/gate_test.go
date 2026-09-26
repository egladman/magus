package ci

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecideGateMatrix pins every cell of the decision matrix by hand. The
// expectations are written out rather than derived, so a change to DecideGate's
// logic fails here instead of being restated by the test.
func TestDecideGateMatrix(t *testing.T) {
	cases := []struct {
		name string
		in   GateFacts
		want GateDecision
	}{
		{"nothing on record", GateFacts{}, GateRun},
		{"not redundant, forced", GateFacts{Forced: true}, GateRun},
		{"not redundant, nested", GateFacts{Nested: true}, GateRun},
		{"not redundant, forced and nested", GateFacts{Forced: true, Nested: true}, GateRun},

		// Redundant refuses on its own; no machine-load condition gates it.
		{"redundant", GateFacts{Redundant: true}, GateRefuse},
		{"redundant, forced", GateFacts{Redundant: true, Forced: true}, GateRun},
		// Nested is the one caller that cannot refuse: it counts its own ancestors'
		// claims as load.
		{"redundant, nested", GateFacts{Redundant: true, Nested: true}, GateAdvise},
		{"redundant, forced and nested", GateFacts{Redundant: true, Forced: true, Nested: true}, GateRun},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, DecideGate(tc.in))
		})
	}
}

func TestPoolSaturated(t *testing.T) {
	cases := []struct {
		name string
		snap *types.MachineSnapshot
		want bool
	}{
		{"nil snapshot fails open", nil, false},
		{"idle", &types.MachineSnapshot{BudgetSlots: 8, HeldSlots: 2}, false},
		{"all slots held", &types.MachineSnapshot{BudgetSlots: 8, HeldSlots: 8}, true},
		{"all memory held", &types.MachineSnapshot{BudgetMB: 1000, HeldMB: 1000}, true},
		{"unlimited axes never saturate", &types.MachineSnapshot{HeldSlots: 100, HeldMB: 100000}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, PoolSaturated(tc.snap))
		})
	}
}

func TestGateFingerprint(t *testing.T) {
	a := []GateStep{{Project: ".", Target: "ci", Key: "k1"}, {Project: "docs", Target: "ci", Key: "k2"}}
	b := []GateStep{{Project: "docs", Target: "ci", Key: "k2"}, {Project: ".", Target: "ci", Key: "k1"}}
	assert.Equal(t, GateFingerprint(a), GateFingerprint(b), "order-insensitive")
	assert.NotEqual(t, GateFingerprint(a), GateFingerprint([]GateStep{{Project: ".", Target: "ci", Key: "k1"}}), "selection is part of the identity")
	assert.NotEqual(t, GateFingerprint(a), GateFingerprint([]GateStep{{Project: ".", Target: "ci", Key: "OTHER"}, {Project: "docs", Target: "ci", Key: "k2"}}), "a changed key changes the fingerprint")
	assert.Empty(t, GateFingerprint(nil), "an empty selection has no identity")
}

func TestMergeFreeRange(t *testing.T) {
	linear := []types.Commit{
		{ID: "c3", Parents: []string{"c2"}},
		{ID: "c2", Parents: []string{"c1"}},
		{ID: "c1", Parents: []string{"c0"}},
	}
	assert.True(t, MergeFreeRange(linear, "c1"), "linear history down to green")
	assert.True(t, MergeFreeRange(linear, "c3"), "green at head is a trivially clean range")
	assert.False(t, MergeFreeRange(linear, "c0"), "green outside the walked window cannot be vouched for")

	// A merge always re-gates, however clean: it combines two verified
	// histories into a tree neither gate saw.
	merged := []types.Commit{
		{ID: "m1", Parents: []string{"c2", "f1"}},
		{ID: "c2", Parents: []string{"c1"}},
		{ID: "c1", Parents: []string{"c0"}},
	}
	assert.False(t, MergeFreeRange(merged, "c1"), "merge between green and head")
	assert.True(t, MergeFreeRange(merged, "m1"), "green AT the merge is behind us, not in the range")
}

// TestPRMergeFreeRange: under a pull-request checkout the head IS a merge the
// provider synthesized, so it is exempt; a merge anyone pushed still refuses.
func TestPRMergeFreeRange(t *testing.T) {
	synthetic := []types.Commit{
		{ID: "pr-merge", Parents: []string{"main1", "c2"}},
		{ID: "c2", Parents: []string{"c1"}},
		{ID: "c1", Parents: []string{"c0"}},
	}
	assert.True(t, PRMergeFreeRange(synthetic, "c1"), "the synthetic head merge is the harness's, not the delta's")
	assert.False(t, MergeFreeRange(synthetic, "c1"), "the plain rule still refuses it")

	pushed := []types.Commit{
		{ID: "pr-merge", Parents: []string{"main1", "m1"}},
		{ID: "m1", Parents: []string{"c2", "f1"}},
		{ID: "c2", Parents: []string{"c1"}},
	}
	assert.False(t, PRMergeFreeRange(pushed, "c1"), "a merge below the synthetic head is in the range")
	assert.True(t, PRMergeFreeRange(synthetic, "pr-merge"), "green at the head is a trivially clean range")
	assert.False(t, PRMergeFreeRange(nil, "c1"), "no history cannot be vouched for")
}

func TestInheritOff(t *testing.T) {
	assert.False(t, InheritOff(nil), "on is the default")
	assert.False(t, InheritOff([]*types.Project{{Path: "."}, {Path: "docs"}}))
	assert.True(t, InheritOff([]*types.Project{{Path: "."}, {Path: "docs", GateInheritOff: true}}),
		"one declaration turns it off workspace-wide")
}

// A dropped option is indistinguishable from a dropped OPT-OUT, and the opt-outs here
// all fail open: an ignored `gate_inherit = false` reads as inheritance on, which skips
// the very CI run the author demanded. So a magusfile this binary could not fully read
// does not get to skip CI.
func TestInheritOffWhenAnOptionWasDropped(t *testing.T) {
	assert.True(t, InheritOff([]*types.Project{
		{Path: "."},
		{Path: "docs", IgnoredOptions: []string{"gate_something_new"}},
	}), "a binary that did not understand the whole magusfile must not inherit a verdict")
}

// newInheritProbe builds a probe whose provider, history and assessment are all stubbed,
// so the decision is exercised with no repository and no CI provider.
func newInheritProbe(green string, found bool, history []types.Commit, rep types.RiskReport) InheritProbe {
	return InheritProbe{
		LastGreenRun: func(context.Context) (string, string, bool) {
			return "https://example/run/7", green, found
		},
		History: func(context.Context) ([]types.Commit, error) { return history, nil },
		Assess:  func(context.Context, string) (types.RiskReport, error) { return rep, nil },
	}
}

var (
	proseReport = types.RiskReport{Tier: types.RiskTrivial, Evidence: []types.RiskEvidence{
		{Path: "docs/x.md", Class: "prose", Tier: types.RiskTrivial, Why: `matches "**/*.md" (built-in default); nothing in ci's chain reads it`},
	}}
	codeReport = types.RiskReport{Tier: types.RiskScoped, Evidence: []types.RiskEvidence{
		{Path: "docs/x.md", Class: "prose", Tier: types.RiskTrivial, Why: "prose"},
		{Path: "internal/y.go", Class: "code", Tier: types.RiskScoped, Why: "Go package y"},
	}}
	// embedReport is the regression the tier exists for: markdown a package compiles in
	// is prose by class, and inheriting over it skipped the tests that read it.
	embedReport = types.RiskReport{Tier: types.RiskScoped, Evidence: []types.RiskEvidence{
		{Path: "internal/agent/skills/run/SKILL.md", Class: "prose", Tier: types.RiskScoped, Why: "embedded by Go package skills (go:embed)"},
	}}
)

var inheritHistory = []types.Commit{
	{ID: "pr-merge", Parents: []string{"main1", "c2"}},
	{ID: "c2", Parents: []string{"green0123456789"}},
	{ID: "green0123456789", Parents: []string{"c0"}},
}

// TestInheritProbeFires pins the whole hit: the finding, and both reports it
// publishes. The report text is spelled out rather than derived, because an
// inherited verdict is only defensible if a reader can reconstruct it; a
// change that thins the report has to fail here.
func TestInheritProbeFires(t *testing.T) {
	var assessedAt string
	p := newInheritProbe("green0123456789", true, inheritHistory, proseReport)
	p.Assess = func(_ context.Context, green string) (types.RiskReport, error) {
		assessedAt = green
		return proseReport, nil
	}
	got, ok := p.Evaluate(context.Background())
	require.True(t, ok)
	assert.Equal(t, InheritFinding{Run: "https://example/run/7", Commit: "green0123456789", Report: proseReport}, got)
	assert.Equal(t, "green0123456789", assessedAt, "the change is measured from the green commit")

	assert.Equal(t, "verdict inherited from run https://example/run/7 (commit green012): "+
		"the change since that green run tiers trivial\n"+
		`docs/x.md: trivial (prose: matches "**/*.md" (built-in default); nothing in ci's chain reads it)`, got.AnnotationText())

	assert.Equal(t, "### Inherited verdict\n\n"+
		"The shard fan-out was short-circuited: the change since this branch's "+
		"last green CI run tiers trivial, so that run's verdict stands.\n\n"+
		"Inherited run: https://example/run/7 at commit `green012`.\n\n"+
		"| Changed path | Tier | Class | Decided by |\n| --- | --- | --- | --- |\n"+
		"| `docs/x.md` | trivial | prose | matches \"**/*.md\" (built-in default); nothing in ci's chain reads it |\n\n"+
		"To dispute a row, its last column names the declaration or mechanism that "+
		"decided it; to turn inheritance off, declare `gate_inherit = false` in "+
		"magus.project.\n", got.SummaryMarkdown())
}

// TestInheritProbeDeclines: every refusal reports ok=false and an empty
// finding, so the plan emits nothing at all from this feature.
func TestInheritProbeDeclines(t *testing.T) {
	pushedMerge := []types.Commit{
		{ID: "pr-merge", Parents: []string{"main1", "m1"}},
		{ID: "m1", Parents: []string{"c2", "f1"}},
		{ID: "c2", Parents: []string{"green0123456789"}},
		{ID: "green0123456789", Parents: []string{"c0"}},
	}
	cases := map[string]InheritProbe{
		"no green run":          newInheritProbe("green0123456789", false, inheritHistory, proseReport),
		"green run unnamed":     newInheritProbe("", true, inheritHistory, proseReport),
		"code in the delta":     newInheritProbe("green0123456789", true, inheritHistory, codeReport),
		"embedded markdown":     newInheritProbe("green0123456789", true, inheritHistory, embedReport),
		"a comment-only change": newInheritProbe("green0123456789", true, inheritHistory, types.RiskReport{Tier: types.RiskMechanical}),
		"merge in the range":    newInheritProbe("green0123456789", true, pushedMerge, proseReport),
		"green out of reach":    newInheritProbe("unreachable", true, inheritHistory, proseReport),
		"disabled":              {Disabled: true},
		"no provider wired":     {},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := p.Evaluate(context.Background())
			assert.False(t, ok)
			assert.Equal(t, InheritFinding{}, got, "a declined probe carries nothing")
		})
	}
}

// A failing history or diff call declines rather than propagating: the plan
// proceeds exactly as it would have without this feature.
func TestInheritProbeSurvivesBrokenInputs(t *testing.T) {
	boom := errors.New("no such revision")
	p := newInheritProbe("green0123456789", true, inheritHistory, proseReport)
	p.History = func(context.Context) ([]types.Commit, error) { return nil, boom }
	_, ok := p.Evaluate(context.Background())
	assert.False(t, ok, "an unreadable history declines")

	p = newInheritProbe("green0123456789", true, inheritHistory, proseReport)
	p.Assess = func(context.Context, string) (types.RiskReport, error) { return proseReport, boom }
	_, ok = p.Evaluate(context.Background())
	assert.False(t, ok, "an unreadable diff declines")
}

// An empty delta is the boundary case: nothing changed since green, so the
// verdict inherits and both reports say so rather than printing an empty table.
func TestInheritProbeEmptyDelta(t *testing.T) {
	p := newInheritProbe("green0123456789", true, inheritHistory, types.RiskReport{Tier: types.RiskTrivial})
	got, ok := p.Evaluate(context.Background())
	require.True(t, ok)
	assert.Contains(t, got.AnnotationText(), "\nno paths changed since that run")
	assert.Contains(t, got.SummaryMarkdown(), "No paths changed since that run.\n")
	assert.NotContains(t, got.SummaryMarkdown(), "| Changed path |")
}
