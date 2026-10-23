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

func TestEnumConformanceSetsClose(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		[]string{
			"file-lines", "func-lines", "type-methods", "file-symbols", "dep-fanout", "clone-tokens", "export-scope",
			"call-sequence", "shared-body", "shared-reach", "caller-spread", "mode-knobs", "alias-share", "name-clash", "main-logic", "store-path-export", "file-split",
		},
		ConformanceMetric("").Values())
	assert.Equal(t, []string{"source", "main", "test"}, ConformancePopulation("").Values())
	assert.Equal(t, []string{"ratchet", "anchor"}, ConformanceSource("").Values())
	assert.Equal(t,
		[]bool{true, false, true, false, true, false},
		[]bool{
			ConformanceCloneTokens.Valid(), ConformanceMetric("clones").Valid(),
			ConformancePopMain.Valid(), ConformancePopulation("vendor").Valid(),
			ConformanceFromAnchor.Valid(), ConformanceSource("live").Valid(),
		})
	assert.Equal(t, "unset", ConformanceMetric("").String())
}
