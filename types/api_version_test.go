package types

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type APIVersionSuite struct {
	suite.Suite
}

func TestAPIVersion(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(APIVersionSuite))
}

func (s *APIVersionSuite) TestParseAcceptsAlpha() {
	v, err := ParseAPIVersion("magus.job/v1alpha1")
	s.Require().NoError(err)
	s.Equal(JobAPIVersion, v)
}

func (s *APIVersionSuite) TestParseAcceptsStable() {
	_, err := ParseAPIVersion("mergequeue.event/v2")
	s.NoError(err)
}

func (s *APIVersionSuite) TestParseRefusesAnInteger() {
	_, err := ParseAPIVersion("13")
	s.EqualError(err, `api version "13" is not <group>/v<major>[alpha<n>|beta<n>], as in "magus.job/v1alpha1"`)
}

func (s *APIVersionSuite) TestParseRefusesAVersionWithNoGroup() {
	_, err := ParseAPIVersion("v1alpha1")
	s.Error(err)
}

func (s *APIVersionSuite) TestParseRefusesAZeroMajor() {
	_, err := ParseAPIVersion("magus.job/v0alpha1")
	s.Error(err)
}

func (s *APIVersionSuite) TestParseRefusesAnUnnumberedAlpha() {
	_, err := ParseAPIVersion("magus.job/v1alpha")
	s.Error(err)
}

func (s *APIVersionSuite) TestGroupAndVersionSplitAtTheSlash() {
	s.Equal("magus.job", JobAPIVersion.Group())
	s.Equal("v1alpha1", JobAPIVersion.Version())
}

func (s *APIVersionSuite) TestMalformedHasNoParts() {
	v := APIVersion("13")
	s.False(v.Valid())
	s.Empty(v.Group())
	s.Empty(v.Version())
	s.Empty(v.Stability())
}

func (s *APIVersionSuite) TestStabilityReadsTheSuffix() {
	s.Equal(StabilityAlpha, APIVersion("magus.job/v1alpha2").Stability())
	s.Equal(StabilityBeta, APIVersion("magus.job/v1beta1").Stability())
	s.Equal(StabilityStable, APIVersion("magus.job/v1").Stability())
}

func (s *APIVersionSuite) TestCompareOrdersKubernetesStyle() {
	ordered := []APIVersion{
		"magus.job/v1alpha1", "magus.job/v1alpha2", "magus.job/v1alpha10",
		"magus.job/v1beta1", "magus.job/v1", "magus.job/v2alpha1", "magus.job/v2",
	}
	for i := 1; i < len(ordered); i++ {
		s.Equal(-1, ordered[i-1].Compare(ordered[i]), "%s < %s", ordered[i-1], ordered[i])
		s.Equal(1, ordered[i].Compare(ordered[i-1]), "%s > %s", ordered[i], ordered[i-1])
	}
}

func (s *APIVersionSuite) TestCompareEqual() {
	s.Zero(JobAPIVersion.Compare("magus.job/v1alpha1"))
}

func (s *APIVersionSuite) TestCompareOrdersByGroupFirst() {
	s.Equal(-1, APIVersion("magus.a/v2").Compare("magus.b/v1alpha1"))
}

func (s *APIVersionSuite) TestCompareSortsMalformedFirst() {
	s.Equal(-1, APIVersion("13").Compare(JobAPIVersion))
}

func (s *APIVersionSuite) TestRefuseAcceptsItsOwnVersion() {
	s.NoError(JobAPIVersion.Refuse("the row", JobAPIVersion, "delete it"))
}

func (s *APIVersionSuite) TestRefuseNamesAMissingVersion() {
	err := JobAPIVersion.Refuse("the row", "", "delete the job store")
	s.EqualError(err, "the row carries no api_version, so an older magus wrote it, "+
		"and this magus reads only magus.job/v1alpha1: delete the job store")
}

func (s *APIVersionSuite) TestRefuseAsksForANewerMagus() {
	err := JobAPIVersion.Refuse("the row", "magus.job/v1alpha2", "delete it")
	s.EqualError(err, "the row is magus.job/v1alpha2, newer than the magus.job/v1alpha1 this magus reads: upgrade magus")
}

func (s *APIVersionSuite) TestRefuseNamesTheFixForAnOlderVersion() {
	err := APIVersion("magus.job/v1alpha2").Refuse("the row", JobAPIVersion, "delete it")
	s.EqualError(err, "the row is magus.job/v1alpha1 and this magus reads only magus.job/v1alpha2: delete it")
}

func (s *APIVersionSuite) TestRefuseNamesTheFixForAnotherGroup() {
	err := JobAPIVersion.Refuse("the row", "magus.event/v9", "delete it")
	s.EqualError(err, "the row is magus.event/v9 and this magus reads only magus.job/v1alpha1: delete it")
}
