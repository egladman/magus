package depgraph

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNoopObserver(t *testing.T) {
	var obs NoopObserver
	// must not panic
	assert.NotPanics(t, func() {
		obs.OnBuild(BuildStats{})
		obs.OnQuery(QueryEvent{})
		obs.OnError(errors.New("ignored"))
	})
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

	assert.Equal(t, 2, buildCalls)
	assert.Equal(t, 2, queryCalls)
	assert.Equal(t, 2, errCalls)
}

type countingObserver struct {
	builds, queries, errs *int
}

func (o *countingObserver) OnBuild(BuildStats) { *o.builds++ }
func (o *countingObserver) OnQuery(QueryEvent) { *o.queries++ }
func (o *countingObserver) OnError(error)      { *o.errs++ }
