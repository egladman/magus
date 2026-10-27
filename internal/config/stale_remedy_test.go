package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStaleRemedyRendersEachCase(t *testing.T) {
	for name, tc := range map[string]struct{ got, want string }{
		"latest release":  {ReleaseCommand(">= 0.4.0"), "magus self update"},
		"pinned release":  {ReleaseCommand("= 0.4.3"), "magus self update --version v0.4.3"},
		"bare pin":        {ReleaseCommand("v0.5.0-rc.3"), "magus self update --version v0.5.0-rc.3"},
		"bounded range":   {ReleaseCommand(">= 0.4.3, < 0.5.0"), "magus self update --version v0.4.3"},
		"foreign module":  {SourceCommand("/w/tool", "cmd/tool", "tool", false), "go -C /w/tool build -o tool ./cmd/tool"},
		"own bootstrap":   {SourceCommand("/w/magus", "", "", true), "GOEXPERIMENT=jsonv2 go -C /w/magus run -trimpath ./cmd/magus run go-build --no-cache ."},
		"quoted checkout": {SourceCommand("/w/a b $c 'd'", "", "", true), `GOEXPERIMENT=jsonv2 go -C "/w/a b \$c 'd'" run -trimpath ./cmd/magus run go-build --no-cache .`},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.got)
		})
	}
}
