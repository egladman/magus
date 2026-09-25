package ci

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/internal/risk"
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

// newInheritProbe builds a probe whose provider, history and delta are all stubbed, so
// the decision is exercised with no repository and no CI provider. The classifier
// carries no blob readers, which is the strict setting: a code language cannot read as
// comment-only by accident.
func newInheritProbe(green string, found bool, history []types.Commit, changed []string) InheritProbe {
	return InheritProbe{
		LastGreenRun: func(context.Context) (string, string, bool) {
			return "https://example/run/7", green, found
		},
		History:    func(context.Context) ([]types.Commit, error) { return history, nil },
		Changed:    func(context.Context, string) ([]string, error) { return changed, nil },
		Classifier: risk.Classifier{Prose: risk.ProseScopes(nil)},
	}
}

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
	p := newInheritProbe("green0123456789", true, inheritHistory, []string{"docs/x.md"})
	got, ok := p.Evaluate(context.Background())
	require.True(t, ok)
	assert.Equal(t, "https://example/run/7", got.Run)
	assert.Equal(t, "green0123456789", got.Commit)
	assert.Equal(t, []risk.Classified{
		{Path: "docs/x.md", Class: risk.ClassProse, Why: `matches "**/*.md" (built-in default)`},
	}, got.Delta.Paths)

	assert.Equal(t, "verdict inherited from run https://example/run/7 (commit green012): "+
		"every path changed since that green run classifies low-risk\n"+
		`docs/x.md: prose (matches "**/*.md" (built-in default))`, got.AnnotationText())

	assert.Equal(t, "### Inherited verdict\n\n"+
		"The shard fan-out was short-circuited: every path changed since this branch's "+
		"last green CI run classifies low-risk, so that run's verdict stands.\n\n"+
		"Inherited run: https://example/run/7 at commit `green012`.\n\n"+
		"| Changed path | Class | Classified by |\n| --- | --- | --- |\n"+
		"| `docs/x.md` | prose | matches \"**/*.md\" (built-in default) |\n\n"+
		"To dispute a row, its last column names the declaration or mechanism that "+
		"classified it; to turn inheritance off, declare `gate_inherit = false` in "+
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
		"no green run":       newInheritProbe("green0123456789", false, inheritHistory, []string{"docs/x.md"}),
		"green run unnamed":  newInheritProbe("", true, inheritHistory, []string{"docs/x.md"}),
		"code in the delta":  newInheritProbe("green0123456789", true, inheritHistory, []string{"docs/x.md", "internal/y.go"}),
		"merge in the range": newInheritProbe("green0123456789", true, pushedMerge, []string{"docs/x.md"}),
		"green out of reach": newInheritProbe("unreachable", true, inheritHistory, []string{"docs/x.md"}),
		"disabled":           {Disabled: true},
		"no provider wired":  {},
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
	p := newInheritProbe("green0123456789", true, inheritHistory, []string{"docs/x.md"})
	p.History = func(context.Context) ([]types.Commit, error) { return nil, boom }
	_, ok := p.Evaluate(context.Background())
	assert.False(t, ok, "an unreadable history declines")

	p = newInheritProbe("green0123456789", true, inheritHistory, nil)
	p.Changed = func(context.Context, string) ([]string, error) { return nil, boom }
	_, ok = p.Evaluate(context.Background())
	assert.False(t, ok, "an unreadable diff declines")
}

// An empty delta is the boundary case: nothing changed since green, so the
// verdict inherits and both reports say so rather than printing an empty table.
func TestInheritProbeEmptyDelta(t *testing.T) {
	p := newInheritProbe("green0123456789", true, inheritHistory, nil)
	got, ok := p.Evaluate(context.Background())
	require.True(t, ok)
	assert.Contains(t, got.AnnotationText(), "\nno paths changed since that run")
	assert.Contains(t, got.SummaryMarkdown(), "No paths changed since that run.\n")
	assert.NotContains(t, got.SummaryMarkdown(), "| Changed path |")
}
