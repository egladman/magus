package types_test

import (
	"errors"
	"testing"

	"github.com/egladman/magus/types"
)

// countingObserver records how many times each callback fired.
type countingObserver struct {
	builds, queries, errs int
}

func (c *countingObserver) OnBuild(types.BuildStats) { c.builds++ }
func (c *countingObserver) OnQuery(types.QueryEvent) { c.queries++ }
func (c *countingObserver) OnError(error)            { c.errs++ }

func TestFanOutNilCollapsesToNoop(t *testing.T) {
	// No observers, and all-nil observers, must both yield a no-op that does
	// not panic when invoked.
	for _, name := range []string{"empty", "all-nil"} {
		t.Run(name, func(t *testing.T) {
			var o types.Observer
			if name == "empty" {
				o = types.FanOut()
			} else {
				o = types.FanOut(nil, nil)
			}
			// Must not panic.
			o.OnBuild(types.BuildStats{})
			o.OnQuery(types.QueryEvent{})
			o.OnError(errors.New("x"))
		})
	}
}

func TestFanOutForwardsToAllLiveObservers(t *testing.T) {
	a, b := &countingObserver{}, &countingObserver{}
	// A nil interleaved among live observers must be skipped silently.
	o := types.FanOut(a, nil, b)

	o.OnBuild(types.BuildStats{})
	o.OnQuery(types.QueryEvent{})
	o.OnQuery(types.QueryEvent{})
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
	o := types.FanOut(a)
	o.OnBuild(types.BuildStats{})
	if a.builds != 1 {
		t.Errorf("builds = %d, want 1", a.builds)
	}
}
