package bindings

import (
	"errors"
	"testing"

	"github.com/egladman/magus/internal/ci/annotate"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/spells/gen/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// opTarget matches an Invoke request for one op, whatever its params: these tests assert
// which op the annotator calls, and the mock answers so it runs without a Buzz VM.
func opTarget(name string) any {
	return mock.MatchedBy(func(req spells.InvokeRequest) bool { return req.Target == name })
}

// An op the spell does not declare is a nil result with no error, and stays a
// no-op: a provider implements only the verbs its host supports.
func TestSpellAnnotatorUndeclaredOpIsNotAFailure(t *testing.T) {
	drv := mocks.NewMockDriver(t)
	for _, op := range []string{"group_start", "group_end", "annotate"} {
		drv.EXPECT().Invoke(mock.Anything, opTarget(op)).Return(spells.InvokeResponse{}, nil).Once()
	}
	a := &spellAnnotator{drv: drv}

	require.NoError(t, a.StartGroup(annotate.Group{ID: "g", Title: "t"}))
	require.NoError(t, a.EndGroup("g"))
	require.NoError(t, a.Annotate(annotate.Annotation{Level: annotate.LevelError, Message: "m"}))
}

// A provider whose handler raises must say so. The error was discarded here, so
// a spell that failed on every annotation reported success for the life of the
// build, and the adapter is the only layer that can tell the two apart.
func TestSpellAnnotatorReportsARealFailure(t *testing.T) {
	boom := errors.New("handler raised")
	drv := mocks.NewMockDriver(t)
	drv.EXPECT().Invoke(mock.Anything, mock.Anything).Return(spells.InvokeResponse{}, boom).Times(3)
	a := &spellAnnotator{drv: drv}

	for name, err := range map[string]error{
		"group_start": a.StartGroup(annotate.Group{ID: "g"}),
		"group_end":   a.EndGroup("g"),
		"annotate":    a.Annotate(annotate.Annotation{Message: "m"}),
	} {
		require.ErrorIsf(t, err, boom, "%s must surface the provider's failure", name)
		assert.ErrorContains(t, err, name, "the error names the op that failed")
	}
}

// The last_green_run contract, decoded. Inheritance is an optimization, so
// every shape but a complete {run, commit} record reads as "no green run",
// including the raised error, which the annotate ops above must NOT swallow.
func TestSpellAnnotatorLastGreenRunDecodesTheContract(t *testing.T) {
	drv := mocks.NewMockDriver(t)
	drv.EXPECT().Invoke(mock.Anything, opTarget("last_green_run")).Return(spells.InvokeResponse{Data: map[string]any{
		"run":    "https://example/run/7",
		"commit": "green0123456789",
	}}, nil).Once()
	got, ok := (&spellAnnotator{drv: drv}).LastGreenRun()
	require.True(t, ok)
	assert.Equal(t, annotate.GreenRun{Run: "https://example/run/7", Commit: "green0123456789"}, got)

	declines := map[string]struct {
		resp spells.InvokeResponse
		err  error
	}{
		"undeclared op":    {},
		"a null answer":    {resp: spells.InvokeResponse{Data: nil}},
		"a raised handler": {err: errors.New("handler raised")},
		"not a record":     {resp: spells.InvokeResponse{Data: "green0123456789"}},
		"no run":           {resp: spells.InvokeResponse{Data: map[string]any{"commit": "green0123456789"}}},
		"no commit":        {resp: spells.InvokeResponse{Data: map[string]any{"run": "https://example/run/7"}}},
		"wrong types":      {resp: spells.InvokeResponse{Data: map[string]any{"run": 7, "commit": 9}}},
	}
	for name, row := range declines {
		t.Run(name, func(t *testing.T) {
			d := mocks.NewMockDriver(t)
			d.EXPECT().Invoke(mock.Anything, opTarget("last_green_run")).Return(row.resp, row.err).Once()
			got, ok := (&spellAnnotator{drv: d}).LastGreenRun()
			assert.False(t, ok)
			assert.Equal(t, annotate.GreenRun{}, got)
		})
	}
}
