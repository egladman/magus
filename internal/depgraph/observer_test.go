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
