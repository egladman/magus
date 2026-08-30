package bindings

import (
	"errors"
	"testing"

	"github.com/egladman/magus/internal/ci/annotate"
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
