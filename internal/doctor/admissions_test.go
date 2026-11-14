package doctor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
)

type BudgetAdmissionsSuite struct{ suite.Suite }

func TestBudgetAdmissions(t *testing.T) { suite.Run(t, new(BudgetAdmissionsSuite)) }

var admissionsNow = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func (s *BudgetAdmissionsSuite) TestCountsRefusalsInTheLastDayOnly() {
	got := gradeBudgetAdmissions([]types.MachineAdmission{
		{At: admissionsNow.Add(-time.Hour), Outcome: types.AdmissionRefused},
		{At: admissionsNow.Add(-23 * time.Hour), Outcome: types.AdmissionRefused},
		{At: admissionsNow.Add(-25 * time.Hour), Outcome: types.AdmissionRefused},
	}, admissionsNow)
	s.Equal(check.OK, got.Status)
	s.Contains(got.Message, "2 refusal(s)")
}

func (s *BudgetAdmissionsSuite) TestCountsWaitsPastAQuarterOfTheirBound() {
	got := gradeBudgetAdmissions([]types.MachineAdmission{
		{At: admissionsNow, Outcome: types.AdmissionSeated, WaitedMS: 300, WaitBoundMS: 1000},
		{At: admissionsNow, Outcome: types.AdmissionSeated, WaitedMS: 250, WaitBoundMS: 1000},
		{At: admissionsNow, Outcome: types.AdmissionSeated, WaitedMS: 100, WaitBoundMS: 1000},
	}, admissionsNow)
	s.Contains(got.Message, "1 wait(s) past a quarter")
}

func (s *BudgetAdmissionsSuite) TestAnEmptyStoreReportsZeros() {
	got := gradeBudgetAdmissions(nil, admissionsNow)
	s.Equal(check.OK, got.Status)
	s.Contains(got.Message, "0 refusal(s)")
}

func (s *BudgetAdmissionsSuite) TestWithoutAReaderTheCheckIsSkipped() {
	got := (&runner{}).checkBudgetAdmissions(context.Background())
	s.Equal(check.OK, got.Status)
	s.Contains(got.Message, "skipped")
}

func (s *BudgetAdmissionsSuite) TestAnUnreadableStoreIsAdviceNotAFailure() {
	ctx := WithAdmissions(context.Background(), func(context.Context) ([]types.MachineAdmission, error) {
		return nil, errors.New("line 3 does not decode")
	})
	got := (&runner{}).checkBudgetAdmissions(ctx)
	s.Equal(check.Advice, got.Status)
	s.Contains(got.Message, "line 3 does not decode")
}
