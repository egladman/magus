package bindings

import (
	"testing"

	"github.com/egladman/magus/internal/ci/annotate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An op the spell does not export stays a no-op: a provider implements only the verbs its
// host supports.
func TestSpellAnnotatorUndeclaredOpIsNotAFailure(t *testing.T) {
	a := &spellAnnotator{drv: buzzProvider(t, `export fun mgs_getName() > str { return "ci"; }`)}

	require.NoError(t, a.StartGroup(annotate.Group{ID: "g", Title: "t"}))
	require.NoError(t, a.EndGroup("g"))
	require.NoError(t, a.Annotate(annotate.Annotation{Level: annotate.LevelError, Message: "m"}))
	assert.True(t, a.Active(), "a provider without enabled() is active wherever it was wired")
}

// A provider whose handler raises must say so. The error was discarded here, so
// a spell that failed on every annotation reported success for the life of the
// build, and the adapter is the only layer that can tell the two apart.
func TestSpellAnnotatorReportsARealFailure(t *testing.T) {
	a := &spellAnnotator{drv: buzzProvider(t, `
export fun group_start(target: Target, req: CIGroup) > Ack !> any { throw "handler raised"; }
export fun group_end(target: Target, req: CIGroupEnd) > Ack !> any { throw "handler raised"; }
export fun annotate(target: Target, req: CIAnnotation) > Ack !> any { throw "handler raised"; }`)}

	for name, err := range map[string]error{
		"group_start": a.StartGroup(annotate.Group{ID: "g"}),
		"group_end":   a.EndGroup("g"),
		"annotate":    a.Annotate(annotate.Annotation{Message: "m"}),
	} {
		require.ErrorContainsf(t, err, "handler raised", "%s must surface the provider's failure", name)
		assert.ErrorContains(t, err, name, "the error names the op that failed")
	}
}

// What the annotator hands an op is the sanitized record, field for field.
func TestSpellAnnotatorHandsTheOpItsRecord(t *testing.T) {
	a := &spellAnnotator{drv: buzzProvider(t, `
export fun annotate(target: Target, req: CIAnnotation) > Ack !> any {
    if (req.level != "error" or req.file != "a.go" or req.line != 3 or req.endCol != 9) {
        throw "got {req.level} {req.file}:{req.line}..{req.endCol}";
    }
    return Ack{ ok = true };
}`)}
	require.NoError(t, a.Annotate(annotate.Annotation{Level: annotate.LevelError, Message: "m", File: "a.go", Line: 3, EndCol: 9}))
}

func TestSpellAnnotatorActiveIsWhatEnabledAnswers(t *testing.T) {
	a := &spellAnnotator{drv: buzzProvider(t, `
export fun enabled(target: Target, req: Empty) > Ack { return Ack{ ok = false }; }`)}
	assert.False(t, a.Active())
}

func TestSpellAnnotatorReadsTheQuotePrefixesOnce(t *testing.T) {
	a := &spellAnnotator{drv: buzzProvider(t, `
export fun quote_prefixes(target: Target, req: Empty) > Strings { return Strings{ items = ["::"] }; }`)}
	assert.Equal(t, []string{"::"}, a.quotePrefixes())
	assert.True(t, a.prefixKnown)
}

// The last_green_run contract, decoded. Inheritance is an optimization, so every shape
// but a complete {run, commit} record reads as "no green run", including the raised
// error, which the annotate ops above must NOT swallow.
func TestSpellAnnotatorLastGreenRunDecodesTheContract(t *testing.T) {
	got, ok := (&spellAnnotator{drv: buzzProvider(t, `
export fun last_green_run(target: Target, req: Empty) > GreenRun {
    return GreenRun{ run = "https://example/run/7", commit = "green0123456789" };
}`)}).LastGreenRun()
	require.True(t, ok)
	assert.Equal(t, annotate.GreenRun{Run: "https://example/run/7", Commit: "green0123456789"}, got)

	declines := map[string]string{
		"undeclared op":    `export fun mgs_getName() > str { return "ci"; }`,
		"a raised handler": `export fun last_green_run(target: Target, req: Empty) > GreenRun !> any { throw "boom"; }`,
		"not a record":     `export fun last_green_run(target: Target, req: Empty) > any { return "green0123456789"; }`,
		"no run":           `export fun last_green_run(target: Target, req: Empty) > GreenRun { return GreenRun{ commit = "green0123456789" }; }`,
		"no commit":        `export fun last_green_run(target: Target, req: Empty) > GreenRun { return GreenRun{ run = "7" }; }`,
		"wrong types":      `export fun last_green_run(target: Target, req: Empty) > any { return {"run": 7, "commit": 9}; }`,
	}
	for name, src := range declines {
		t.Run(name, func(t *testing.T) {
			got, ok := (&spellAnnotator{drv: buzzProvider(t, src)}).LastGreenRun()
			assert.False(t, ok)
			assert.Equal(t, annotate.GreenRun{}, got)
		})
	}
}
