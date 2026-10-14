package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnumPrecedentFamilyClosesItsSet(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		[]string{
			"dep-direction", "dep-fanout", "err-sentinel-name", "test-package-name", "param-name-by-type",
			"param-order", "ctx-first", "error-last",
		},
		PrecedentFamily("").Values())
	assert.Equal(t,
		map[PrecedentFamily]bool{"": true, PrecedentDepFanout: true, "dep-fan-out": false},
		map[PrecedentFamily]bool{"": PrecedentFamily("").Valid(), PrecedentDepFanout: PrecedentDepFanout.Valid(), "dep-fan-out": PrecedentFamily("dep-fan-out").Valid()})
	assert.Equal(t, "unset", PrecedentFamily("").String())
}

func TestEnumConformSetsClose(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		[]string{"file-lines", "func-lines", "type-methods", "file-symbols", "dep-fanout", "clone-tokens", "export-scope", "call-sequence"},
		ConformMetric("").Values())
	assert.Equal(t, []string{"source", "main", "test"}, ConformPopulation("").Values())
	assert.Equal(t, []string{"ratchet", "anchor"}, ConformSource("").Values())
	assert.Equal(t,
		[]bool{true, false, true, false, true, false},
		[]bool{
			ConformCloneTokens.Valid(), ConformMetric("clones").Valid(),
			ConformPopMain.Valid(), ConformPopulation("vendor").Valid(),
			ConformFromAnchor.Valid(), ConformSource("live").Valid(),
		})
	assert.Equal(t, "unset", ConformMetric("").String())
}
