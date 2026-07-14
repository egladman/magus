package proc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
)

func newJobService(handler func(ctx context.Context, args []string) error) *service {
	return &service{
		handler:   handler,
		parentCtx: context.Background(),
		lim:       cache.NewLimiter(4),
	}
}

func TestSubmitJobRunsAsync(t *testing.T) {
	ran := make(chan []string, 1)
	s := newJobService(func(_ context.Context, args []string) error {
		ran <- args
		return nil
	})

	var reply JobReply
	require.NoError(t, s.submitJob(JobRequest{Magic: JobMagic, Args: []string{"graph", "build"}}, &reply))
	assert.NotEmpty(t, reply.Inv, "an accepted job returns an invocation id")

	select {
	case got := <-ran:
		assert.Equal(t, []string{"graph", "build"}, got, "the handler runs with the submitted args")
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not run")
	}
}

func TestSubmitJobIgnoresBadMagic(t *testing.T) {
	var ran bool
	s := newJobService(func(context.Context, []string) error { ran = true; return nil })
	var reply JobReply
	require.NoError(t, s.submitJob(JobRequest{Magic: "wrong", Args: []string{"x"}}, &reply))
	assert.Empty(t, reply.Inv, "an unauthenticated submission is ignored")
	time.Sleep(50 * time.Millisecond)
	assert.False(t, ran, "the handler never runs for a bad-magic request")
}

func TestSubmitJobCoalescesDuplicates(t *testing.T) {
	var mu sync.Mutex
	starts := 0
	release := make(chan struct{})
	s := newJobService(func(context.Context, []string) error {
		mu.Lock()
		starts++
		mu.Unlock()
		<-release // hold the first job in flight
		return nil
	})

	req := JobRequest{Magic: JobMagic, Args: []string{"graph", "build"}}
	var r1, r2 JobReply
	require.NoError(t, s.submitJob(req, &r1))
	// Give the first job's goroutine a moment to register as in-flight.
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, s.submitJob(req, &r2))

	assert.NotEmpty(t, r1.Inv, "the first identical job is accepted")
	assert.Empty(t, r2.Inv, "a duplicate in-flight job is coalesced, not re-run")

	close(release)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, starts, "the handler ran once for two identical submissions")
}
