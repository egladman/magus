package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStaleRemedyRendersEachCase(t *testing.T) {
	for name, tc := range map[string]struct {
		remedy StaleRemedy
		want   []string
	}{
		"latest release":  {ReleaseRemedy(">= 0.4.0"), []string{"magus self update"}},
		"pinned release":  {ReleaseRemedy("= 0.4.3"), []string{"magus self update --version v0.4.3"}},
		"bare pin":        {ReleaseRemedy("v0.5.0-rc.3"), []string{"magus self update --version v0.5.0-rc.3"}},
		"skewed daemon":   {StaleRemedy{Case: StaleSkew}, []string{"magus server stop", "magus server start"}},
		"foreign module":  {StaleRemedy{Case: StaleOwnSource, Checkout: "/w/tool", Package: "cmd/tool", Output: "tool"}, []string{"go -C /w/tool build -o tool ./cmd/tool"}},
		"own bootstrap":   {StaleRemedy{Case: StaleOwnSource, Checkout: "/w/magus", Bootstrap: true}, []string{"GOEXPERIMENT=jsonv2 go -C /w/magus run -trimpath ./cmd/magus run go-build --no-cache ."}},
		"quoted checkout": {StaleRemedy{Case: StaleOwnSource, Checkout: "/w/a b $c 'd'", Bootstrap: true}, []string{`GOEXPERIMENT=jsonv2 go -C "/w/a b \$c 'd'" run -trimpath ./cmd/magus run go-build --no-cache .`}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.remedy.Lines())
		})
	}
}

// A range leaves the choice to the release index; only one admitted release is named.
func TestPinnedVersionNamesOnlyAnExactPin(t *testing.T) {
	assert.Equal(t, "v1.2.3", PinnedVersion("1.2.3"))
	assert.Equal(t, "v1.2.3", PinnedVersion("== v1.2.3"))
	assert.Empty(t, PinnedVersion(">= 1.2.3"))
	assert.Empty(t, PinnedVersion(">= 1.2.0, < 1.3.0"))
	assert.Empty(t, PinnedVersion(""))
}
