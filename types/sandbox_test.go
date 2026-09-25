package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxModeUnmarshalText(t *testing.T) {
	for _, name := range []string{"off", "best-effort", "required", ""} {
		var m SandboxMode
		require.NoError(t, m.UnmarshalText([]byte(name)), name)
		assert.Equal(t, SandboxMode(name), m)
	}
	for _, typo := range []string{"on", "true", "1", "Required", "best_effort"} {
		m := SandboxModeBestEffort
		assert.ErrorContains(t, m.UnmarshalText([]byte(typo)), "unknown sandbox mode", typo)
		assert.Equal(t, SandboxModeBestEffort, m, "a refused name leaves the mode alone")
	}
}

func TestSandboxModeZeroValueIsOff(t *testing.T) {
	var m SandboxMode
	assert.Equal(t, SandboxModeOff, m.Resolved())
	assert.Equal(t, "off", m.String())
	assert.False(t, m.Enabled())
	assert.True(t, SandboxModeBestEffort.Enabled())
	assert.True(t, SandboxModeRequired.Enabled())
	assert.Equal(t, []string{"off", "best-effort", "required"}, m.Values())
}

func TestSandboxModeWeakerThan(t *testing.T) {
	var unset SandboxMode
	assert.True(t, SandboxModeOff.WeakerThan(SandboxModeBestEffort))
	assert.True(t, SandboxModeBestEffort.WeakerThan(SandboxModeRequired))
	assert.True(t, unset.WeakerThan(SandboxModeBestEffort), "unset behaves as off")
	assert.False(t, SandboxModeRequired.WeakerThan(SandboxModeBestEffort))
	assert.False(t, SandboxModeBestEffort.WeakerThan(SandboxModeBestEffort))
	assert.False(t, unset.WeakerThan(SandboxModeOff))
}
