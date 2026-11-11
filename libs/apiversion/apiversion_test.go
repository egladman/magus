package apiversion

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type VersionSuite struct{ suite.Suite }

func TestVersionSuite(t *testing.T) { suite.Run(t, new(VersionSuite)) }

func (s *VersionSuite) TestParseAcceptsDottedGroup() {
	v, err := Parse("example.widget/v1alpha1")
	s.Require().NoError(err)
	s.Equal(APIVersion("example.widget/v1alpha1"), v)
}

func (s *VersionSuite) TestParseRejectsUndottedGroup() {
	_, err := Parse("widget/v1alpha1")
	s.Error(err)
}

func (s *VersionSuite) TestParseRejectsUppercaseGroup() {
	_, err := Parse("Example.widget/v1")
	s.Error(err)
}

func (s *VersionSuite) TestParseRejectsLeadingHyphenLabel() {
	_, err := Parse("-example.widget/v1")
	s.Error(err)
}

func (s *VersionSuite) TestParseRejectsBareAlpha() {
	_, err := Parse("example.widget/v1alpha")
	s.Error(err)
}

func (s *VersionSuite) TestParseRejectsOverlongGroup() {
	_, err := Parse(strings.Repeat("abcdefghij.", 24) + "widget/v1")
	s.Error(err)
}

func (s *VersionSuite) TestParseErrorNamesNeutralExample() {
	_, err := Parse("nope")
	s.Require().Error(err)
	s.Contains(err.Error(), "example.widget/v1")
}

func (s *VersionSuite) TestGroupVersionStability() {
	v := APIVersion("example.widget/v2beta3")
	s.Equal("example.widget", v.Group())
	s.Equal("v2beta3", v.Version())
	s.Equal(StabilityBeta, v.Stability())
}

func (s *VersionSuite) TestMalformedHasNoComponents() {
	v := APIVersion("nope")
	s.Empty(v.Group())
	s.Empty(v.Version())
	s.Empty(v.Stability())
	s.False(v.Valid())
}

func (s *VersionSuite) TestStableStability() {
	s.Equal(StabilityStable, APIVersion("example.widget/v1").Stability())
}

func (s *VersionSuite) TestCompareOrdersWithinGroup() {
	order := []APIVersion{
		"example.widget/v1alpha1", "example.widget/v1alpha2", "example.widget/v1beta1",
		"example.widget/v1", "example.widget/v2alpha1",
	}
	for i := 1; i < len(order); i++ {
		s.Equal(-1, order[i-1].Compare(order[i]))
		s.Equal(1, order[i].Compare(order[i-1]))
	}
	s.Equal(0, order[0].Compare(order[0]))
}

func (s *VersionSuite) TestCompareOrdersGroupsFirst() {
	s.Equal(-1, APIVersion("a.example/v2").Compare("b.example/v1"))
}

func (s *VersionSuite) TestCompareSortsMalformedFirst() {
	s.Equal(-1, APIVersion("nope").Compare("example.widget/v1"))
}

func (s *VersionSuite) TestRefuseNilWhenEqual() {
	s.NoError(APIVersion("example.widget/v1").Refuse("example.widget/v1"))
}

func (s *VersionSuite) TestRefuseNewerMatchesNewerSentinel() {
	s.ErrorIs(APIVersion("example.widget/v1").Refuse("example.widget/v2"), ErrNewer)
}

func (s *VersionSuite) TestRefuseOtherGroupIsNotNewer() {
	err := APIVersion("example.widget/v1").Refuse("example.gadget/v2")
	s.Require().Error(err)
	s.NotErrorIs(err, ErrNewer)
}

func (s *VersionSuite) TestRefuseMissingVersion() {
	err := APIVersion("example.widget/v1").Refuse("")
	s.Require().Error(err)
	s.Contains(err.Error(), "no apiVersion")
}
