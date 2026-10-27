package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStaleRemedyRendersEachCase(t *testing.T) {
	for name, tc := range map[string]struct {
		remedy staleRemedy
		want   []string
	}{
		"latest release":  {ReleaseRemedy(">= 0.4.0"), []string{"magus self update"}},
		"pinned release":  {ReleaseRemedy("= 0.4.3"), []string{"magus self update --version v0.4.3"}},
		"bare pin":        {ReleaseRemedy("v0.5.0-rc.3"), []string{"magus self update --version v0.5.0-rc.3"}},
		"bounded range":   {ReleaseRemedy(">= 0.4.3, < 0.5.0"), []string{"magus self update --version v0.4.3"}},
		"foreign module":  {SourceRemedy("/w/tool", "cmd/tool", "tool", false), []string{"go -C /w/tool build -o tool ./cmd/tool"}},
		"own bootstrap":   {SourceRemedy("/w/magus", "", "", true), []string{"GOEXPERIMENT=jsonv2 go -C /w/magus run -trimpath ./cmd/magus run go-build --no-cache ."}},
		"quoted checkout": {SourceRemedy("/w/a b $c 'd'", "", "", true), []string{`GOEXPERIMENT=jsonv2 go -C "/w/a b \$c 'd'" run -trimpath ./cmd/magus run go-build --no-cache .`}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.remedy.Lines())
		})
	}
}
