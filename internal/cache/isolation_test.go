package cache

import (
	"context"
	"testing"
	"time"
)

func TestYieldRunIsolationLetsExclusiveChildRun(t *testing.T) {
	ctx, releaseParent := acquireRunIsolation(WithRunScope(context.Background()), false)
	t.Cleanup(releaseParent)
	parentLease := isolationLeaseFrom(ctx)

	childAcquired := make(chan struct{})
	releaseChild := make(chan struct{})
	childDone := make(chan struct{})
	go func() {
		_, release := acquireRunIsolation(ctx, true)
		close(childAcquired)
		<-releaseChild
		release()
		close(childDone)
	}()

	err := YieldRunIsolation(ctx, func(yielded context.Context) error {
		if got := isolationLeaseFrom(yielded); got != nil {
			t.Errorf("yielded context retained lease: %#v", got)
		}
		select {
		case <-childAcquired:
		case <-time.After(time.Second):
			t.Error("exclusive child did not acquire isolation while parent yielded")
		}
		close(releaseChild)
		select {
		case <-childDone:
		case <-time.After(time.Second):
			t.Error("exclusive child did not release isolation")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("YieldRunIsolation: %v", err)
	}
	if got := isolationLeaseFrom(ctx); got != parentLease {
		t.Fatalf("parent lease after yield = %#v, want %#v", got, parentLease)
	}
}
