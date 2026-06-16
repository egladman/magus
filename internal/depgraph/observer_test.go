package depgraph

import (
	"errors"
	"testing"
)

func TestNoopObserver(t *testing.T) {
	var obs NoopObserver
	// must not panic
	obs.OnBuild(BuildStats{})
	obs.OnQuery(QueryEvent{})
	obs.OnError(errors.New("ignored"))
}

func TestFanOut_Propagates(t *testing.T) {
	var buildCalls, queryCalls, errCalls int

	obs := FanOut(
		&countingObserver{builds: &buildCalls, queries: &queryCalls, errs: &errCalls},
		&countingObserver{builds: &buildCalls, queries: &queryCalls, errs: &errCalls},
	)

	obs.OnBuild(BuildStats{})
	obs.OnQuery(QueryEvent{})
	obs.OnError(errors.New("test"))

	if buildCalls != 2 {
		t.Errorf("OnBuild calls = %d, want 2", buildCalls)
	}
	if queryCalls != 2 {
		t.Errorf("OnQuery calls = %d, want 2", queryCalls)
	}
	if errCalls != 2 {
		t.Errorf("OnError calls = %d, want 2", errCalls)
	}
}

type countingObserver struct {
	builds, queries, errs *int
}

func (o *countingObserver) OnBuild(BuildStats) { *o.builds++ }
func (o *countingObserver) OnQuery(QueryEvent) { *o.queries++ }
func (o *countingObserver) OnError(error)      { *o.errs++ }
