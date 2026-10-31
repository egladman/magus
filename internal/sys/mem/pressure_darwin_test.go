//go:build darwin

package mem

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type DarwinPressureSuite struct {
	suite.Suite
}

func TestDarwinPressureSuite(t *testing.T) {
	suite.Run(t, new(DarwinPressureSuite))
}

// The panic read 73% of compressed pages (OK) and 100% of segments (BAD).
// Only segments are an input, so the 73% cannot excuse it.
func (s *DarwinPressureSuite) TestThePanicReadingRefuses() {
	const limit = 1216383
	got := darwinPressure(1, limit, limit)
	s.True(got.Bad)
	s.Contains(got.State, "100% of its segment limit")
}

func (s *DarwinPressureSuite) TestAHealthyMachineReadingAdmits() {
	s.False(darwinPressure(1, 103509, 1216383).Bad, "8.5% of the limit, read from a working machine")
}

func (s *DarwinPressureSuite) TestTheThresholdRefuses() {
	s.True(darwinPressure(1, 75, 100).Bad)
	s.False(darwinPressure(1, 74, 100).Bad)
}

func (s *DarwinPressureSuite) TestCriticalLevelRefusesWithARoomyCompressor() {
	got := darwinPressure(pressureLevelCritical, 1, 100)
	s.True(got.Bad)
	s.Contains(got.State, "critical memory pressure")
}

func (s *DarwinPressureSuite) TestWarningLevelAdmits() {
	s.False(darwinPressure(2, 1, 100).Bad)
}

func (s *DarwinPressureSuite) TestUnknownSegmentsLeaveTheLevelToAnswer() {
	s.False(darwinPressure(1, 0, 0).Bad)
	s.True(darwinPressure(pressureLevelCritical, 0, 0).Bad)
}

// A sysctl decoded at the wrong width still yields a number.
func (s *DarwinPressureSuite) TestTheLiveSysctlsDecode() {
	limit := sysctlCount("vm.compressor.segment.limit")
	s.Positive(limit)
	s.LessOrEqual(sysctlCount("vm.compressor.segment.total"), limit)
	_, err := ReadPressure(s.T().Context())
	s.Require().NoError(err)
}

func (s *DarwinPressureSuite) TestAnUnknownSysctlReadsZero() {
	s.Zero(sysctlCount("vm.compressor.segment.absent"))
}
