package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnumPrecedentFamilyClosesItsSet(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		[]string{"dep-direction", "dep-fanout", "err-sentinel-name", "test-package-name"},
		PrecedentFamily("").Values())
	assert.Equal(t,
		map[PrecedentFamily]bool{"": true, PrecedentDepFanout: true, "dep-fan-out": false},
		map[PrecedentFamily]bool{"": PrecedentFamily("").Valid(), PrecedentDepFanout: PrecedentDepFanout.Valid(), "dep-fan-out": PrecedentFamily("dep-fan-out").Valid()})
	assert.Equal(t, "unset", PrecedentFamily("").String())
}
