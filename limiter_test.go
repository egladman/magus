package magus

import (
	"testing"

	"github.com/egladman/magus/internal/workspace"
	"github.com/stretchr/testify/assert"
)

func TestLimiterFacade(t *testing.T) {
	limiter := NewLimiter(3)
	var load workspace.Load
	WithLimiter(limiter)(&load)

	assert.Equal(t, struct {
		Capacity      int
		Default       int
		InjectedLimit any
	}{
		Capacity:      3,
		Default:       DefaultConcurrency(),
		InjectedLimit: limiter.lim,
	}, struct {
		Capacity      int
		Default       int
		InjectedLimit any
	}{
		Capacity:      limiter.Capacity(),
		Default:       DefaultConcurrency(),
		InjectedLimit: load.Limiter,
	})
}
