package proc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTickIntervalScalesWithTheTimeout(t *testing.T) {
	assert.Equal(t, 10*time.Second, tickInterval(30*time.Second))
	assert.Equal(t, 10*time.Second, tickInterval(time.Minute))
	assert.Equal(t, 30*time.Second, tickInterval(2*time.Minute))
	assert.Equal(t, 30*time.Second, tickInterval(5*time.Minute))
	assert.Equal(t, time.Minute, tickInterval(10*time.Minute))
}

func TestWithTimeoutExpiresTheContext(t *testing.T) {
	ctx, cancel := WithTimeout(context.Background(), 20*time.Millisecond, "test")
	defer cancel()
	select {
	case <-ctx.Done():
		assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	case <-time.After(5 * time.Second):
		t.Fatal("the deadline never fired")
	}
}
