package e2e

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type suiteSuite struct{ suite.Suite }

func TestSuite(t *testing.T) { suite.Run(t, new(suiteSuite)) }

// A base with Test methods runs them for each suite embedding it, and an
// embedding suite replaces one by declaring the same name.
type innerBase struct {
	Suite
	ran *[]string
}

func (s *innerBase) TestPromotedFromTheBase() {
	*s.ran = append(*s.ran, "promoted")
}

func (s *innerBase) TestShadowedByTheOuterSuite() {
	*s.ran = append(*s.ran, "base shadow")
}

type outerSuite struct{ innerBase }

func (s *outerSuite) TestShadowedByTheOuterSuite() {
	*s.ran = append(*s.ran, "outer shadow")
}

func (s *suiteSuite) TestRunsPromotedMethodsAndTheOuterShadow() {
	var ran []string
	outer := &outerSuite{innerBase{ran: &ran}}
	s.Run("inner", func() { suite.Run(s.T(), outer) })
	s.ElementsMatch([]string{"promoted", "outer shadow"}, ran)
}

type ownerProbe struct {
	Suite
	seenOwner, seenMethod *testing.T
}

func (s *ownerProbe) TestSeesAnOwnerAboveItself() {
	s.seenOwner, s.seenMethod = s.Owner(), s.T()
}

func (s *suiteSuite) TestOwnerIsTheTestRunWasGiven() {
	probe := new(ownerProbe)
	var given *testing.T
	s.Run("inner", func() {
		given = s.T()
		suite.Run(given, probe)
	})
	s.Same(given, probe.seenOwner)
	s.NotSame(given, probe.seenMethod)
}

type binaryProbe struct {
	Suite
	binary Binary
	after  bool
}

func (s *binaryProbe) TestRequiresIt() {
	s.RequireBinary(s.binary)
	s.after = true
}

func (s *suiteSuite) TestRequireBinaryContinuesWhenFound() {
	probe := &binaryProbe{binary: Binary{Name: "x", Path: "/x"}}
	s.Run("inner", func() { suite.Run(s.T(), probe) })
	s.True(probe.after)
}

func (s *suiteSuite) TestRequireBinarySkipsWhenAbsent() {
	probe := &binaryProbe{binary: Binary{Name: "x", Note: "absent"}}
	s.Run("inner", func() { suite.Run(s.T(), probe) })
	s.False(probe.after)
	s.False(s.T().Failed())
}
