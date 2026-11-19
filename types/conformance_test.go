package types

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

var (
	fileLinesSource        = ConformanceKey{Metric: ConformanceFileLines, Population: ConformancePopSource}
	importedPackagesSource = ConformanceKey{Metric: ConformanceImportedPackages, Population: ConformancePopSource}
)

const conformanceMetricList = "[file-lines func-lines type-methods file-symbols imported-packages clone-tokens export-scope " +
	"call-sequence shared-body shared-reach caller-spread mode-knobs alias-share name-clash main-logic store-path-export file-split]"

type ConformanceSuite struct {
	suite.Suite
	o ConformanceOptions
}

func TestConformanceSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(ConformanceSuite))
}

func (s *ConformanceSuite) SetupTest() {
	s.o = ConformanceOptions{
		Limits: map[ConformanceKey]ConformanceLimit{
			fileLinesSource:        {Percentile: 95, Anchor: 2200},
			importedPackagesSource: {Percentile: 95, Anchor: 62},
		},
		MinSample: 30,
		Step:      0.05,
		Gates:     ConformanceGates{MinCohort: 5, MinShare: 0.8, Bar: 0.5, MaxFindings: 5},
	}
}

func (s *ConformanceSuite) TestEffectiveIsTheConfiguredAnchor() {
	v, ok := s.o.Effective(fileLinesSource)
	s.True(ok)
	s.Equal(2200, v)
}

func (s *ConformanceSuite) TestEffectiveFallsBackFromAScopeToTheUnscopedKey() {
	v, ok := s.o.Effective(ConformanceKey{Metric: ConformanceImportedPackages, Population: ConformancePopSource, Scope: "internal"})
	s.True(ok)
	s.Equal(62, v)
}

func (s *ConformanceSuite) TestEffectivePrefersTheScopesOwnAnchor() {
	scoped := ConformanceKey{Metric: ConformanceImportedPackages, Population: ConformancePopSource, Scope: "cmd/magus"}
	s.o.Limits[scoped] = ConformanceLimit{Anchor: 89}
	v, ok := s.o.Effective(scoped)
	s.True(ok)
	s.Equal(89, v)
}

func (s *ConformanceSuite) TestEffectiveWithNothingConfigured() {
	v, ok := s.o.Effective(ConformanceKey{Metric: ConformanceTypeMethods, Population: ConformancePopTest})
	s.False(ok)
	s.Zero(v)
}

func (s *ConformanceSuite) TestMetricPastForACeiling() {
	s.True(ConformanceFileLines.Past(1501, 1500))
	s.False(ConformanceFileLines.Past(1500, 1500))
}

func (s *ConformanceSuite) TestMetricPastForTheFileSplitFloor() {
	s.True(ConformanceFileSplit.Past(1500, 1500))
	s.False(ConformanceFileSplit.Past(1501, 1500))
}

func (s *ConformanceSuite) TestValidateAcceptsAConfiguredPolicy() {
	s.NoError(s.o.Validate())
}

func (s *ConformanceSuite) TestValidateRefusesALimitWithAnUnknownMetric() {
	s.o.Limits[ConformanceKey{Metric: "bogus", Population: ConformancePopSource}] = ConformanceLimit{Anchor: 1}
	s.EqualError(s.o.Validate(), "limit bogus source: metric bogus: want one of "+conformanceMetricList)
}

func (s *ConformanceSuite) TestValidateRefusesALimitWithAnUnknownPopulation() {
	s.o.Limits[ConformanceKey{Metric: ConformanceFileLines, Population: "bogus"}] = ConformanceLimit{Anchor: 1}
	s.EqualError(s.o.Validate(), "limit file-lines bogus: population bogus: want one of [source main test]")
}

func (s *ConformanceSuite) TestValidateRefusesAPercentilePast100() {
	s.o.Limits[fileLinesSource] = ConformanceLimit{Percentile: 101, Anchor: 2200}
	s.EqualError(s.o.Validate(), "limit file-lines source: percentile 101: want 0 through 100")
}

func (s *ConformanceSuite) TestValidateRefusesANegativeAnchor() {
	s.o.Limits[importedPackagesSource] = ConformanceLimit{Percentile: 95, Anchor: -1}
	s.EqualError(s.o.Validate(), "limit imported-packages source: anchor -1: want at least 0")
}

func (s *ConformanceSuite) TestValidateRefusesAMinSampleBelow1() {
	s.o.MinSample = 0
	s.EqualError(s.o.Validate(), "min_sample 0: want at least 1")
}

func (s *ConformanceSuite) TestValidateRefusesAStepOf1() {
	s.o.Step = 1
	s.EqualError(s.o.Validate(), "step 1: want at least 0 and below 1")
}

func (s *ConformanceSuite) TestValidateRefusesANegativeStep() {
	s.o.Step = -0.05
	s.EqualError(s.o.Validate(), "step -0.05: want at least 0 and below 1")
}

func (s *ConformanceSuite) TestValidateRefusesAMinCohortBelow1() {
	s.o.Gates.MinCohort = 0
	s.EqualError(s.o.Validate(), "gates.min_cohort 0: want at least 1")
}

func (s *ConformanceSuite) TestValidateRefusesAMinSharePast1() {
	s.o.Gates.MinShare = 80
	s.EqualError(s.o.Validate(), "gates.min_share 80: want 0 through 1")
}

func (s *ConformanceSuite) TestValidateRefusesANegativeBar() {
	s.o.Gates.Bar = -0.5
	s.EqualError(s.o.Validate(), "gates.bar -0.5: want 0 through 1")
}

func (s *ConformanceSuite) TestValidateRefusesNegativeMaxFindings() {
	s.o.Gates.MaxFindings = -1
	s.EqualError(s.o.Validate(), "gates.max_findings -1: want at least 0")
}

func (s *ConformanceSuite) TestValidateReportsEveryProblem() {
	s.o.MinSample = 0
	s.o.Step = 1
	s.EqualError(s.o.Validate(), "min_sample 0: want at least 1\nstep 1: want at least 0 and below 1")
}
