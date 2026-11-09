package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

// CheckoutHookAtSuite reads checkout.bootstrap from a magus.yaml a strict load
// may refuse.
type CheckoutHookAtSuite struct {
	suite.Suite
	root string
}

func TestCheckoutHookAtSuite(t *testing.T) {
	suite.Run(t, new(CheckoutHookAtSuite))
}

func (s *CheckoutHookAtSuite) SetupTest() {
	s.root = s.T().TempDir()
}

func (s *CheckoutHookAtSuite) write(yaml string) {
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, Filename), []byte(yaml), 0o644))
}

func (s *CheckoutHookAtSuite) TestItReadsTheHookPastAKeyThisBinaryLacks() {
	s.write("required_version: \">= 9.0.0\"\nno_such_key: 1\ncheckout:\n  bootstrap: hack/dev/./boot.buzz\n")
	s.Equal("hack/dev/boot.buzz", CheckoutHookAt(s.root))
}

func (s *CheckoutHookAtSuite) TestAHookLeavingTheWorkspaceIsNone() {
	s.write("checkout:\n  bootstrap: ../boot.buzz\n")
	s.Empty(CheckoutHookAt(s.root))
}

func (s *CheckoutHookAtSuite) TestNoMagusYAMLIsNone() {
	s.Empty(CheckoutHookAt(s.root))
}

func (s *CheckoutHookAtSuite) TestTheReadyLineRunsTheHookHere() {
	s.Equal("./magus buzz hack/dev/boot.buzz -- --here --verify-ready", ReadyCheckoutCommand("hack/dev/boot.buzz"))
}

func (s *CheckoutHookAtSuite) TestNoHookHasNoReadyLine() {
	s.Empty(ReadyCheckoutCommand(""))
}

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
