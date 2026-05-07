package graph_test

import (
	"errors"
	"testing"

	"github.com/egladman/magus/internal/graph"
)

func TestNoopObserver(t *testing.T) {
	var obs graph.NoopObserver
	// must not panic
	obs.OnBuild(graph.BuildStats{})
	obs.OnQuery(graph.QueryEvent{})
	obs.OnError(errors.New("ignored"))
}

func TestFanOut_Propagates(t *testing.T) {
	var buildCalls, queryCalls, errCalls int

	obs := graph.FanOut(
		&countingObserver{builds: &buildCalls, queries: &queryCalls, errs: &errCalls},
		&countingObserver{builds: &buildCalls, queries: &queryCalls, errs: &errCalls},
	)

	obs.OnBuild(graph.BuildStats{})
	obs.OnQuery(graph.QueryEvent{})
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

func (o *countingObserver) OnBuild(graph.BuildStats)  { *o.builds++ }
func (o *countingObserver) OnQuery(graph.QueryEvent)   { *o.queries++ }
func (o *countingObserver) OnError(error)              { *o.errs++ }
