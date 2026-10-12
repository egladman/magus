package types

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

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
	boom := errors.New("boom")
	a, b := NewMockObserver(t), NewMockObserver(t)
	for _, o := range []*MockObserver{a, b} {
		o.EXPECT().OnBuild(BuildStats{}).Once()
		o.EXPECT().OnQuery(QueryEvent{}).Times(2)
		o.EXPECT().OnError(boom).Once()
	}
	// A nil interleaved among live observers must be skipped silently.
	o := FanOut(a, nil, b)

	o.OnBuild(BuildStats{})
	o.OnQuery(QueryEvent{})
	o.OnQuery(QueryEvent{})
	o.OnError(boom)
}

func TestFanOutSingleObserverPassThrough(t *testing.T) {
	a := NewMockObserver(t)
	a.EXPECT().OnBuild(mock.Anything).Once()
	FanOut(a).OnBuild(BuildStats{})
}

// NoopObserver's callbacks are empty; invoking each must be a safe no-op.
func TestNoopObserver(t *testing.T) {
	assert.NotPanics(t, func() {
		var o NoopObserver
		o.OnBuild(BuildStats{})
		o.OnQuery(QueryEvent{})
		o.OnError(errors.New("x"))
	})
}
