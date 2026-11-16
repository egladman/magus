package budget

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type PriceSuite struct{ suite.Suite }

func TestPrice(t *testing.T) { suite.Run(t, new(PriceSuite)) }

func (s *PriceSuite) TestMultiplierStepsAboveTheThreshold() {
	stepped := Price{LongContext: &PriceStep{Above: 100_000, Multiplier: 5}}
	s.InDelta(1.0, stepped.Multiplier(100_000), 0, "the step is above the threshold, not at it")
	s.InDelta(5.0, stepped.Multiplier(100_001), 0)
}

func (s *PriceSuite) TestMultiplierIsOneWithoutAWholeStep() {
	s.InDelta(1.0, Price{}.Multiplier(900_000), 0, "a nil LongContext is no step")
	s.InDelta(1.0, Price{LongContext: &PriceStep{Above: 100_000}}.Multiplier(900_000), 0, "a step without a multiplier surcharges nothing")
	s.InDelta(1.0, Price{LongContext: &PriceStep{Multiplier: 5}}.Multiplier(900_000), 0, "a step without a threshold surcharges nothing")
}
