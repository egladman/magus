package proc

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSubOpZeroValue(t *testing.T) {
	var o SubOp
	assert.Empty(t, o.Load())
}

func TestSubOpSetLoad(t *testing.T) {
	o := &SubOp{}
	o.Set("archive.uncompress foo.tar.zst [4×]")
	assert.Equal(t, "archive.uncompress foo.tar.zst [4×]", o.Load())
}

func TestSubOpSetEmptyClears(t *testing.T) {
	o := &SubOp{}
	o.Set("something")
	o.Set("")
	assert.Empty(t, o.Load())
}

func TestSubOpNilSafe(t *testing.T) {
	var o *SubOp
	// Neither call should panic.
	assert.NotPanics(t, func() {
		o.Set("label")
		assert.Empty(t, o.Load())
	})
}

func TestSubOpConcurrent(t *testing.T) {
	o := &SubOp{}
	const writers = 8
	const iters = 1000

	var wg sync.WaitGroup
	// Concurrent writers.
	for i := range writers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for range iters {
				o.Set("label")
				o.Set("")
			}
			_ = id
		}(i)
	}
	// Concurrent readers racing against writers.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range writers * iters {
			_ = o.Load()
		}
	}()
	wg.Wait()
}

func TestWithSubOpRoundTrip(t *testing.T) {
	o := &SubOp{}
	ctx := WithSubOp(context.Background(), o)
	assert.Same(t, o, SubOpFromContext(ctx))
}

func TestSubOpFromContextMissing(t *testing.T) {
	got := SubOpFromContext(context.Background())
	assert.Nil(t, got)
	// nil result must be safe to use directly.
	assert.NotPanics(t, func() {
		got.Set("label")
		assert.Empty(t, got.Load())
	})
}

func TestSubOpFromContextWrongType(t *testing.T) {
	type otherKey struct{}
	ctx := context.WithValue(context.Background(), otherKey{}, "not a SubOp")
	assert.Nil(t, SubOpFromContext(ctx))
}
