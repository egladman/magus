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
		FanOut().Observe(GraphEvent{Op: GraphOpBuild})
	})
	assert.NotPanics(t, func() {
		FanOut(nil, nil).Observe(GraphEvent{Op: GraphOpBuild, Err: errors.New("x")})
	})
}

func TestFanOutForwardsToAllLiveObservers(t *testing.T) {
	build := GraphEvent{Op: GraphOpBuild}
	query := GraphEvent{Op: "affected"}
	a, b := NewMockObserver(t), NewMockObserver(t)
	for _, o := range []*MockObserver{a, b} {
		o.EXPECT().Observe(build).Once()
		o.EXPECT().Observe(query).Times(2)
	}
	// A nil interleaved among live observers must be skipped silently.
	o := FanOut(a, nil, b)

	o.Observe(build)
	o.Observe(query)
	o.Observe(query)
}

func TestFanOutSingleObserverPassThrough(t *testing.T) {
	a := NewMockObserver(t)
	a.EXPECT().Observe(mock.Anything).Once()
	FanOut(a).Observe(GraphEvent{Op: GraphOpBuild})
}

// NoopObserver's callback is empty; invoking it must be a safe no-op.
func TestNoopObserver(t *testing.T) {
	assert.NotPanics(t, func() {
		var o NoopObserver
		o.Observe(GraphEvent{Op: GraphOpBuild, Err: errors.New("x")})
	})
}
