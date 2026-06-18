package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVCSErrorSentinels(t *testing.T) {
	for _, sentinel := range []error{ErrVCSUnsupported, ErrVCSUnknown} {
		assert.NotNil(t, sentinel)
		assert.NotEmpty(t, sentinel.Error())
		assert.ErrorIs(t, sentinel, sentinel)
	}
}

func TestVCSSourceConstants(t *testing.T) {
	sources := []VCSSource{
		VCSSourceExplicit,
		VCSSourceAuto,
		VCSSourceDefault,
		VCSSourceDisabled,
	}
	seen := map[VCSSource]bool{}
	for _, s := range sources {
		assert.NotEmpty(t, string(s), "VCSSource constant is empty")
		assert.Falsef(t, seen[s], "duplicate VCSSource value %q", s)
		seen[s] = true
	}
}

func TestVCSResolution_ZeroValue(t *testing.T) {
	var r VCSResolution
	assert.Equal(t, VCSResolution{}, r)
	assert.Nil(t, r.VCS)
	assert.Empty(t, r.Name)
}
