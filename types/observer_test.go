package types

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// countingObserver records how many times each callback fired.
type countingObserver struct {
	builds, queries, errs int
}

func (c *countingObserver) OnBuild(BuildStats) { c.builds++ }
func (c *countingObserver) OnQuery(QueryEvent) { c.queries++ }
func (c *countingObserver) OnError(error)      { c.errs++ }

// No observers, and all-nil observers, must both yield a no-op that does not
// panic when invoked.
func TestFanOutNilCollapsesToNoop(t *testing.T) {
	assert.NotPanics(t, func() {
		o := FanOut()
		o.OnBuild(BuildStats{})
		o.OnQuery(QueryEvent{})
		o.OnError(errors.New("x"))
	})
	assert.NotPanics(t, func() {
		o := FanOut(nil, nil)
		o.OnBuild(BuildStats{})
		o.OnQuery(QueryEvent{})
		o.OnError(errors.New("x"))
	})
}

func TestFanOutForwardsToAllLiveObservers(t *testing.T) {
	a, b := &countingObserver{}, &countingObserver{}
	// A nil interleaved among live observers must be skipped silently.
	o := FanOut(a, nil, b)

	o.OnBuild(BuildStats{})
	o.OnQuery(QueryEvent{})
	o.OnQuery(QueryEvent{})
	o.OnError(errors.New("boom"))

	want := &countingObserver{builds: 1, queries: 2, errs: 1}
	assert.Equal(t, want, a)
	assert.Equal(t, want, b)
}

func TestFanOutSingleObserverPassThrough(t *testing.T) {
	a := &countingObserver{}
	o := FanOut(a)
	o.OnBuild(BuildStats{})
	assert.Equal(t, 1, a.builds)
}
