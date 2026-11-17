package job

import (
	"context"
	"fmt"
	"strings"
)

// Report is a run of a check goal's check the caller describes and the store did not see.
type Report struct {
	Job, Goal string
	Check     LeaseCheck
	Exit      int
	// By names who ran it. Required: a report nobody signs for is unattributable.
	By string
}

// ReportError is a [Report] that names no one who ran it.
type ReportError struct{ Job, Goal string }

func (e *ReportError) Error() string {
	return fmt.Sprintf("job: the report of %s goal %q names no one who ran the check", e.Job, e.Goal)
}

// ReportRun records r as a run of the tree as it is now and returns its ref. It grades
// only a goal that accepts [BasisReported]; every other goal reads it as a violation,
// never as a pass. Named ReportRun so a caller's store that embeds this one keeps its own
// Report.
//
// A report with no By is a [*ReportError], one naming a goal its job does not declare as
// a check a [*GoalError], and a store opened without [WithTree] refuses with a
// [*GradingUnconfiguredError].
func (s *Store) ReportRun(ctx context.Context, r Report) (RunRef, error) {
	if err := s.needTree("ReportRun"); err != nil {
		return "", err
	}
	if strings.TrimSpace(r.By) == "" {
		return "", s.wrap(&ReportError{Job: r.Job, Goal: r.Goal})
	}
	if _, _, err := s.checkGoal(r.Job, r.Goal); err != nil {
		return "", err
	}
	tree, err := s.digestTree(ctx, "for the report")
	if err != nil {
		return "", err
	}
	now := s.Now()
	return s.record(ctx, run{
		job: r.Job, goal: r.Goal, basis: BasisReported, check: r.Check, tree: tree, exit: r.Exit,
		started: now, ended: now, by: strings.TrimSpace(r.By),
	})
}
