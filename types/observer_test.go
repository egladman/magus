package types

import (
	"errors"
	"testing"
)

// countingObserver records how many times each callback fired.
type countingObserver struct {
	builds, queries, errs int
}

func (c *countingObserver) OnBuild(BuildStats) { c.builds++ }
func (c *countingObserver) OnQuery(QueryEvent) { c.queries++ }
func (c *countingObserver) OnError(error)      { c.errs++ }

func TestFanOutNilCollapsesToNoop(t *testing.T) {
	// No observers, and all-nil observers, must both yield a no-op that does
	// not panic when invoked.
	for _, name := range []string{"empty", "all-nil"} {
		t.Run(name, func(t *testing.T) {
			var o Observer
			if name == "empty" {
				o = FanOut()
			} else {
				o = FanOut(nil, nil)
			}
			// Must not panic.
			o.OnBuild(BuildStats{})
			o.OnQuery(QueryEvent{})
			o.OnError(errors.New("x"))
		})
	}
}

func TestFanOutForwardsToAllLiveObservers(t *testing.T) {
	a, b := &countingObserver{}, &countingObserver{}
	// A nil interleaved among live observers must be skipped silently.
	o := FanOut(a, nil, b)

	o.OnBuild(BuildStats{})
	o.OnQuery(QueryEvent{})
	o.OnQuery(QueryEvent{})
	o.OnError(errors.New("boom"))

	for _, obs := range []*countingObserver{a, b} {
		if obs.builds != 1 || obs.queries != 2 || obs.errs != 1 {
			t.Errorf("observer = {builds:%d queries:%d errs:%d}, want {1 2 1}",
				obs.builds, obs.queries, obs.errs)
		}
	}
}

func TestFanOutSingleObserverPassThrough(t *testing.T) {
	a := &countingObserver{}
	o := FanOut(a)
	o.OnBuild(BuildStats{})
	if a.builds != 1 {
		t.Errorf("builds = %d, want 1", a.builds)
	}
}
