package bindings

import (
	"errors"
	"testing"

	"github.com/egladman/magus/internal/ci/annotate"
	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An op the spell does not declare is a nil result with no error, and stays a
// no-op: a provider implements only the verbs its host supports.
func TestSpellAnnotatorUndeclaredOpIsNotAFailure(t *testing.T) {
	drv := &stubDriver{}
	a := &spellAnnotator{drv: drv}

	assert.NoError(t, a.StartGroup(annotate.Group{ID: "g", Title: "t"}))
	assert.Equal(t, "group_start", drv.got.Target)
	assert.NoError(t, a.EndGroup("g"))
	assert.Equal(t, "group_end", drv.got.Target)
	assert.NoError(t, a.Annotate(annotate.Annotation{Level: annotate.LevelError, Message: "m"}))
	assert.Equal(t, "annotate", drv.got.Target)
}

// A provider whose handler raises must say so. The error was discarded here, so
// a spell that failed on every annotation reported success for the life of the
// build - and the adapter is the only layer that can tell the two apart.
func TestSpellAnnotatorReportsARealFailure(t *testing.T) {
	boom := errors.New("handler raised")
	a := &spellAnnotator{drv: &stubDriver{err: boom}}

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
// every shape but a complete {run, commit} record reads as "no green run" -
// including the raised error, which the annotate ops above must NOT swallow.
func TestSpellAnnotatorLastGreenRunDecodesTheContract(t *testing.T) {
	drv := &stubDriver{resp: spells.InvokeResponse{Data: map[string]any{
		"run":    "https://example/run/7",
		"commit": "green0123456789",
	}}}
	got, ok := (&spellAnnotator{drv: drv}).LastGreenRun()
	require.True(t, ok)
	assert.Equal(t, annotate.GreenRun{Run: "https://example/run/7", Commit: "green0123456789"}, got)
	assert.Equal(t, "last_green_run", drv.got.Target)

	declines := map[string]*stubDriver{
		"undeclared op":    {},
		"a null answer":    {resp: spells.InvokeResponse{Data: nil}},
		"a raised handler": {err: errors.New("handler raised")},
		"not a record":     {resp: spells.InvokeResponse{Data: "green0123456789"}},
		"no run":           {resp: spells.InvokeResponse{Data: map[string]any{"commit": "green0123456789"}}},
		"no commit":        {resp: spells.InvokeResponse{Data: map[string]any{"run": "https://example/run/7"}}},
		"wrong types":      {resp: spells.InvokeResponse{Data: map[string]any{"run": 7, "commit": 9}}},
	}
	for name, d := range declines {
		t.Run(name, func(t *testing.T) {
			got, ok := (&spellAnnotator{drv: d}).LastGreenRun()
			assert.False(t, ok)
			assert.Equal(t, annotate.GreenRun{}, got)
		})
	}
}
