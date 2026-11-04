//go:build darwin

package mem

import (
	"errors"
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
	s.Contains(darwinPressure(1, limit, limit), "100% of its segment limit")
}

func (s *DarwinPressureSuite) TestAHealthyMachineReadingAdmits() {
	s.Empty(darwinPressure(1, 103509, 1216383), "8.5% of the limit, read from a working machine")
}

func (s *DarwinPressureSuite) TestTheThresholdRefuses() {
	s.NotEmpty(darwinPressure(1, 75, 100))
	s.Empty(darwinPressure(1, 74, 100))
}

func (s *DarwinPressureSuite) TestCriticalLevelRefusesWithARoomyCompressor() {
	s.Contains(darwinPressure(pressureLevelCritical, 1, 100), "critical memory pressure")
}

func (s *DarwinPressureSuite) TestWarningLevelAdmits() {
	s.Empty(darwinPressure(2, 1, 100))
}

func (s *DarwinPressureSuite) TestUnknownSegmentsLeaveTheLevelToAnswer() {
	s.Empty(darwinPressure(1, 0, 0))
	s.NotEmpty(darwinPressure(pressureLevelCritical, 0, 0))
}

func (s *DarwinPressureSuite) TestAnUnreadableSegmentCountIsNamed() {
	segments, limit, err := compressorSegments(func(name string) (int64, error) {
		if name == "vm.compressor.segment.limit" {
			return 0, errors.New("no such sysctl")
		}
		return 9, nil
	})
	s.Require().ErrorContains(err, "the kernel's level answers alone")
	s.Zero(segments, "half a pair is no reading")
	s.Zero(limit)
}

// A sysctl decoded at the wrong width still yields a number.
func (s *DarwinPressureSuite) TestTheLiveSysctlsDecode() {
	segments, limit, err := compressorSegments(sysctlCount)
	s.Require().NoError(err)
	s.Positive(limit)
	s.LessOrEqual(segments, limit)
	_, err = ReadPressure(s.T().Context())
	s.Require().NoError(err)
}

func (s *DarwinPressureSuite) TestAnUnknownSysctlIsAnError() {
	_, err := sysctlCount("vm.compressor.segment.absent")
	s.Error(err)
}
