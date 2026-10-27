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
		"foreign module":  {SourceRemedy("/w/tool", "cmd/tool", "tool", false), []string{"go -C /w/tool build -o tool ./cmd/tool"}},
		"own bootstrap":   {SourceRemedy("/w/magus", "", "", true), []string{"GOEXPERIMENT=jsonv2 go -C /w/magus run -trimpath ./cmd/magus run go-build --no-cache ."}},
		"quoted checkout": {SourceRemedy("/w/a b $c 'd'", "", "", true), []string{`GOEXPERIMENT=jsonv2 go -C "/w/a b \$c 'd'" run -trimpath ./cmd/magus run go-build --no-cache .`}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.remedy.Lines())
		})
	}
}

// A range leaves the choice to the release index; only one admitted release is named.
func TestPinnedVersionNamesOnlyAnExactPin(t *testing.T) {
	assert.Equal(t, "v1.2.3", pinnedVersion("1.2.3"))
	assert.Equal(t, "v1.2.3", pinnedVersion("== v1.2.3"))
	assert.Empty(t, pinnedVersion(">= 1.2.3"))
	assert.Empty(t, pinnedVersion(">= 1.2.0, < 1.3.0"))
	assert.Empty(t, pinnedVersion(""))
}
