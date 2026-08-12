package magus

import (
	"bytes"
	"errors"
	"io"
	"testing"

	json "github.com/egladman/magus/internal/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/types"
)

// recordedOutputOverlap decodes one report.OutputOverlapDetected JSONL line. The
// wire format splices schema/type and the event's own fields into one flat object
// (see envelope.writeJSONL), not a nested "body".
type recordedOutputOverlap struct {
	Type        string   `json:"type"`
	ProjectA    string   `json:"project_a"`
	ProjectB    string   `json:"project_b"`
	Target      string   `json:"target"`
	Overlapping []string `json:"overlapping"`
}

func recordOutputOverlapEvents(t *testing.T, steps []cache.Step) []recordedOutputOverlap {
	t.Helper()
	var buf bytes.Buffer
	w := report.NewWriter(&buf)
	checkOutputOverlap(steps, w)
	require.NoError(t, w.Close())

	var out []recordedOutputOverlap
	dec := json.NewDecoder(&buf)
	for {
		var ev recordedOutputOverlap
		err := dec.Decode(&ev)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if ev.Type == report.TypeOutputOverlapDetected {
			out = append(out, ev)
		}
	}
	return out
}

// TestCheckOutputOverlap_UsesStepTargetNotScopeLabel is P1-B's regression test for
// checkOutputOverlap: the function used to take the whole invocation's scope label
// (e.g. "3 projects") as its "target" parameter and stamp that into MGS4002 reports,
// even though every cache.Step already carries its own real Target. Pre-fix, calling
// checkOutputOverlap(steps, "3 projects", w) recorded Target: "3 projects" - never
// "build", which is what this test now asserts.
func TestCheckOutputOverlap_UsesStepTargetNotScopeLabel(t *testing.T) {
	steps := []cache.Step{
		{ProjectPath: "a", Target: "build", Outputs: []string{"dist/**"}},
		{ProjectPath: "b", Target: "build", Outputs: []string{"dist/**"}},
	}

	evs := recordOutputOverlapEvents(t, steps)

	require.Len(t, evs, 1)
	assert.Equal(t, "a", evs[0].ProjectA)
	assert.Equal(t, "b", evs[0].ProjectB)
	assert.Equal(t, "build", evs[0].Target, "must carry the steps' real target, not an invocation-wide scope label")
	assert.Equal(t, []string{"dist/**"}, evs[0].Overlapping)
}

// TestCheckOutputOverlap_DifferingTargetsReportsBoth covers the case that made a
// single blanket "target" parameter actively wrong: one executeStages call can cover
// several target stages (runResolved groups a multi-target request into one call), so
// two overlapping steps can legitimately belong to different targets. Neither target
// alone is "the" target - both must be visible in the report.
func TestCheckOutputOverlap_DifferingTargetsReportsBoth(t *testing.T) {
	steps := []cache.Step{
		{ProjectPath: "a", Target: "build", Outputs: []string{"dist/**"}},
		{ProjectPath: "b", Target: "test", Outputs: []string{"dist/**"}},
	}

	evs := recordOutputOverlapEvents(t, steps)

	require.Len(t, evs, 1)
	assert.Equal(t, "build,test", evs[0].Target)
}

// recordedMissingDependency decodes one report.MissingDependency JSONL line (see the
// flat-envelope note on recordedOutputOverlap).
type recordedMissingDependency struct {
	Type     string `json:"type"`
	Consumer string `json:"consumer"`
	Producer string `json:"producer"`
	Path     string `json:"path"`
	Target   string `json:"target"`
}

// TestCheckMissingDependencies_ReportsScopeLabelAsTarget covers the other half of
// P1-B: unlike checkOutputOverlap, no per-write target exists here (written comes from
// race.Runtime.WrittenPaths, which is keyed by project only), so the fix renamed the
// parameter from "target" to "scope" and documented why, rather than fabricating a
// target value. This pins that the report's Target field still carries the scope
// label - the value genuinely available - so a caller reading it is not left with an
// empty field.
func TestCheckMissingDependencies_ReportsScopeLabelAsTarget(t *testing.T) {
	consumer := &types.Project{Path: "consumer", Dir: "/ws/consumer", Sources: []string{"**/*.go"}}
	written := map[string][]string{"producer": {"/ws/consumer/generated.go"}}

	var buf bytes.Buffer
	w := report.NewWriter(&buf)
	checkMissingDependencies([]*types.Project{consumer}, map[string]*types.Project{}, written, "3 projects", w)
	require.NoError(t, w.Close())

	var evs []recordedMissingDependency
	dec := json.NewDecoder(&buf)
	for {
		var ev recordedMissingDependency
		err := dec.Decode(&ev)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if ev.Type == report.TypeMissingDependency {
			evs = append(evs, ev)
		}
	}

	require.Len(t, evs, 1)
	assert.Equal(t, "consumer", evs[0].Consumer)
	assert.Equal(t, "producer", evs[0].Producer)
	assert.Equal(t, "3 projects", evs[0].Target, "scope label is the best available identifier and must still reach the report")
}
