package spells

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type discoverySuite struct{ suite.Suite }

func TestDiscoverySuite(t *testing.T) { suite.Run(t, new(discoverySuite)) }

func (s *discoverySuite) TestTargetsRunInPhaseOrderWithoutCI() {
	s.Equal([]string{"install", "generate", "format", "lint", "build", "test", "security"}, DiscoveryTargets())
}

func (s *discoverySuite) TestTargetsReturnsACopy() {
	got := DiscoveryTargets()
	got[0] = "ci"
	s.Equal("install", DiscoveryTargets()[0])
}

func (s *discoverySuite) TestRoleNamesBothCases() {
	s.Equal([]string{"project", "member"}, DiscoveryRole("").Values())
	s.True(DiscoveryRoleMember.Valid())
	s.False(DiscoveryRole("workspace").Valid())
}
