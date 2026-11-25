package types

import (
	"testing"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/stretchr/testify/suite"
)

type APIVersionSuite struct {
	suite.Suite
}

func TestAPIVersion(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(APIVersionSuite))
}

func (s *APIVersionSuite) TestValidAcceptsAlpha() {
	s.True(APIVersion(job.APIVersion).Valid())
}

func (s *APIVersionSuite) TestValidAcceptsStable() {
	s.True(APIVersion("mergequeue.event/v2").Valid())
}

func (s *APIVersionSuite) TestValidRefusesAnInteger() {
	s.False(APIVersion("13").Valid())
}

func (s *APIVersionSuite) TestValidRefusesAVersionWithNoGroup() {
	s.False(APIVersion("v1alpha1").Valid())
}

func (s *APIVersionSuite) TestValidRefusesASingleLabelGroup() {
	s.False(APIVersion("magus/v1alpha1").Valid())
}

func (s *APIVersionSuite) TestValidRefusesAZeroMajor() {
	s.False(APIVersion("safetyscissors.job/v0alpha1").Valid())
}

func (s *APIVersionSuite) TestValidRefusesAnUnnumberedAlpha() {
	s.False(APIVersion("safetyscissors.job/v1alpha").Valid())
}

func (s *APIVersionSuite) TestGroupAndVersionSplitAtTheSlash() {
	s.Equal("safetyscissors.job", APIVersion(job.APIVersion).Group())
	s.Equal("v1alpha1", APIVersion(job.APIVersion).Version())
}

func (s *APIVersionSuite) TestMalformedHasNoParts() {
	v := APIVersion("13")
	s.False(v.Valid())
	s.Empty(v.Group())
	s.Empty(v.Version())
	s.Empty(v.Stability())
}

func (s *APIVersionSuite) TestStabilityReadsTheSuffix() {
	s.Equal(StabilityAlpha, APIVersion("safetyscissors.job/v1alpha2").Stability())
	s.Equal(StabilityBeta, APIVersion("safetyscissors.job/v1beta1").Stability())
	s.Equal(StabilityStable, APIVersion("safetyscissors.job/v1").Stability())
}

func (s *APIVersionSuite) TestCompareOrdersKubernetesStyle() {
	ordered := []APIVersion{
		"safetyscissors.job/v1alpha1", "safetyscissors.job/v1alpha2", "safetyscissors.job/v1alpha10",
		"safetyscissors.job/v1beta1", "safetyscissors.job/v1", "safetyscissors.job/v2alpha1", "safetyscissors.job/v2",
	}
	for i := 1; i < len(ordered); i++ {
		s.Equal(-1, ordered[i-1].Compare(ordered[i]), "%s < %s", ordered[i-1], ordered[i])
		s.Equal(1, ordered[i].Compare(ordered[i-1]), "%s > %s", ordered[i], ordered[i-1])
	}
}

func (s *APIVersionSuite) TestCompareEqual() {
	s.Zero(APIVersion(job.APIVersion).Compare("safetyscissors.job/v1alpha1"))
}

func (s *APIVersionSuite) TestCompareOrdersByGroupFirst() {
	s.Equal(-1, APIVersion("magus.a/v2").Compare("magus.b/v1alpha1"))
}

func (s *APIVersionSuite) TestCompareSortsMalformedFirst() {
	s.Equal(-1, APIVersion("13").Compare(APIVersion(job.APIVersion)))
}

func (s *APIVersionSuite) TestRefuseVersionAcceptsItsOwnVersion() {
	s.NoError(RefuseVersion(APIVersion(job.APIVersion), APIVersion(job.APIVersion), "the row", "delete it"))
}

func (s *APIVersionSuite) TestRefuseVersionNamesAMissingVersion() {
	err := RefuseVersion(APIVersion(job.APIVersion), "", "the row", "delete the job store")
	s.EqualError(err, "the row: record carries no api_version, and this reader reads only safetyscissors.job/v1alpha1: delete the job store")
}

func (s *APIVersionSuite) TestRefuseVersionAsksForANewerMagus() {
	err := RefuseVersion(APIVersion(job.APIVersion), "safetyscissors.job/v1alpha2", "the row", "delete it")
	s.EqualError(err, "the row: record is safetyscissors.job/v1alpha2, newer than the safetyscissors.job/v1alpha1 this reader reads: upgrade magus")
}

func (s *APIVersionSuite) TestRefuseVersionNamesTheFixForAnOlderVersion() {
	err := RefuseVersion("safetyscissors.job/v1alpha2", APIVersion(job.APIVersion), "the row", "delete it")
	s.EqualError(err, "the row: record is safetyscissors.job/v1alpha1, and this reader reads only safetyscissors.job/v1alpha2: delete it")
}

func (s *APIVersionSuite) TestRefuseVersionNamesTheFixForAnotherGroup() {
	err := RefuseVersion(APIVersion(job.APIVersion), "magus.event/v9", "the row", "delete it")
	s.EqualError(err, "the row: record is magus.event/v9, and this reader reads only safetyscissors.job/v1alpha1: delete it")
}
