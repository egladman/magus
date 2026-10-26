package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

const execMessage = "`Exec` (157 lines) is shared by 6 packages 3 import hops apart (cmd/termcast, internal/a, internal/b, ...), " +
	"and `CancelGrace` is set only by internal/queue, `TTYCols` is set only by cmd/termcast, `TTYRows` is set only by cmd/termcast. " +
	"Share it only when its callers have the same failure semantics and policy knobs, so a bug in one is a bug in the other; " +
	"otherwise give each caller its own copy with one line saying why, or record why it stays shared as a mode-knobs known entry"

func execID() string { return symbolID(conformanceNS(runPkg) + "Exec().") }

func TestCouplingCheckReportsTheExecShape(t *testing.T) {
	g := couplingGraph(execShape()...)

	got := g.ConformanceChecks(ConformanceChange{Subjects: []string{execID()}, Conformance: couplingOptions()})

	assert.Equal(t, map[string][]types.Check{execID(): {advice(types.CheckSharedLoop, execMessage,
		"callers: cmd/termcast, internal/a, internal/b, internal/c, internal/queue, std",
		"shared-body source: 30, the configured anchor",
		"caller-spread source: 2, the configured anchor",
		"shared-reach source: 4, the configured anchor",
	)}}, got)
}

func TestCouplingCheckScoresTheMergeHigher(t *testing.T) {
	g := couplingGraph(execShape()...)
	for _, tc := range []struct {
		name       string
		introduced map[string]bool
		want       float64
	}{
		{name: "an existing function re-signed", want: overSharedScore},
		{name: "the change is the merge", introduced: map[string]bool{execID(): true}, want: overSharedIntroducedScore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newNamingIndex(g, ConformanceChange{Subjects: []string{execID()}, Introduced: tc.introduced, Conformance: couplingOptions()})
			found := (couplingCheck{}).run(x)
			require.Len(t, found, 1)
			assert.InDelta(t, tc.want, found[0].score, 0)
		})
	}
}

func TestCouplingCheckSkipsAKnownEntry(t *testing.T) {
	g := couplingGraph(execShape()...)
	o := couplingOptions()
	o.Ratchet = types.ConformanceRatchet{Schema: 1, Known: []types.ConformanceKnown{{
		Metric: types.ConformanceModeKnobs, Items: []string{"internal/proc/run/exec.go#Exec"},
		Reason: "one process primitive: TTY sizing is termcast's knob, CancelGrace the queue's",
	}}}

	assert.Empty(t, g.ConformanceChecks(ConformanceChange{Subjects: []string{execID()}, Conformance: o}))
}

// What the doctrine keeps shared stays silent: a small utility far callers share, a decision
// table, an edge flag between near callers, and a helper whose parameter is the caller's code.
func TestCouplingCheckIsSilentOnWhatStaysShared(t *testing.T) {
	files := couplingFiles()
	for _, tc := range []struct {
		name string
		fn   types.KnowledgeSymbol
	}{
		{
			name: "a stdlib-like wrapper",
			fn:   couplingFunction("/internal/json", "Marshal", "internal/json/json.go", 10, 10, "func Marshal(v any) ([]byte, error)", nil, files...),
		},
		{
			name: "a decision table, whose branches the index never records",
			fn:   couplingFunction("/internal/json", "Remedy", "internal/json/json.go", 20, 80, "func Remedy(kind string) string", nil, files...),
		},
		{
			name: "an edge flag between near callers",
			fn: couplingFunction("/internal/json", "NeutralizeTerminal", "internal/json/json.go", 100, 146,
				"func NeutralizeTerminal(s string, keepSGR bool) string", []string{"keepSGR"},
				couplingFile("/internal/a"), couplingFile("/internal/b")),
		},
		{
			name: "a helper taking the caller's code",
			fn: couplingFunction("/internal/json", "boundedPhase", "internal/json/json.go", 150, 190,
				"func boundedPhase(ctx context.Context, phase string, call func(context.Context) error) error", []string{"ok"}, files...),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := couplingGraph(conformancePkg("/internal/json", []string{"internal/json/json.go"}), tc.fn)
			id := symbolID(tc.fn.Key)

			got := g.ConformanceChecks(ConformanceChange{Subjects: []string{id}, Introduced: map[string]bool{id: true}, Conformance: couplingOptions()})

			assert.Empty(t, got)
		})
	}
}

// A function two files of one package call, re-signed to branch on a new bool, is two
// functions under one name: the loadWorkspaceGraph shape.
func TestCouplingCheckReportsABranchForOneCaller(t *testing.T) {
	const graphGo, queryGo = "cmd/magus/graph.go", "cmd/magus/query.go"
	load := couplingFunction("/cmd/magus", "loadWorkspaceGraph", graphGo, 647, 700,
		"func loadWorkspaceGraph(ctx context.Context, root string, global bool) (*Graph, error)", []string{"global"}, graphGo, queryGo)
	g := couplingGraph(conformancePkg("/cmd/magus", []string{graphGo, queryGo}), load)
	id := symbolID(load.Key)

	t.Run("re-signed", func(t *testing.T) {
		got := g.ConformanceChecks(ConformanceChange{Subjects: []string{id}, Conformance: couplingOptions()})

		assert.Equal(t, map[string][]types.Check{id: {advice(types.CheckSharedLoop,
			"`loadWorkspaceGraph` now branches on `global`, and 2 files call it (cmd/magus/graph.go, cmd/magus/query.go): "+
				"a branch only one caller takes is a second function under one name. Keep the shared part small and give the branch "+
				"its own function, unless every caller needs the fix it carries",
		)}}, got)
	})

	t.Run("added", func(t *testing.T) {
		assert.Empty(t, g.ConformanceChecks(ConformanceChange{Subjects: []string{id}, Introduced: map[string]bool{id: true}, Conformance: couplingOptions()}),
			"a new function bends nothing that was shared before")
	})
}

func TestCouplingCheckIsDeterministic(t *testing.T) {
	change := ConformanceChange{Subjects: []string{execID()}, Conformance: couplingOptions()}
	first := couplingGraph(execShape()...).ConformanceChecks(change)
	require.NotEmpty(t, first)
	for range 10 {
		assert.Equal(t, first, couplingGraph(execShape()...).ConformanceChecks(change))
	}
}

func TestCouplingCheckIsSilentWithoutASymbolIndex(t *testing.T) {
	assert.Empty(t, NewGraph().ConformanceChecks(ConformanceChange{Subjects: []string{execID()}, Conformance: couplingOptions()}))
}
