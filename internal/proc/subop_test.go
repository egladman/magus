package proc

import (
	"context"
	"sync"
	"testing"
)

func TestSubOpZeroValue(t *testing.T) {
	var o SubOp
	if got := o.Load(); got != "" {
		t.Fatalf("zero value Load() = %q, want %q", got, "")
	}
}

func TestSubOpSetLoad(t *testing.T) {
	o := &SubOp{}
	o.Set("archive.uncompress foo.tar.zst [4×]")
	if got := o.Load(); got != "archive.uncompress foo.tar.zst [4×]" {
		t.Fatalf("Load() = %q, want label", got)
	}
}

func TestSubOpSetEmptyClears(t *testing.T) {
	o := &SubOp{}
	o.Set("something")
	o.Set("")
	if got := o.Load(); got != "" {
		t.Fatalf("Load() after Set(%q) = %q, want %q", "", got, "")
	}
}

func TestSubOpNilSafe(t *testing.T) {
	var o *SubOp
	// Neither call should panic.
	o.Set("label")
	if got := o.Load(); got != "" {
		t.Fatalf("nil Load() = %q, want %q", got, "")
	}
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
	got := SubOpFromContext(ctx)
	if got != o {
		t.Fatalf("SubOpFromContext returned different pointer")
	}
}

func TestSubOpFromContextMissing(t *testing.T) {
	got := SubOpFromContext(context.Background())
	if got != nil {
		t.Fatalf("SubOpFromContext on empty ctx = %v, want nil", got)
	}
	// nil result must be safe to use directly.
	got.Set("label")
	if s := got.Load(); s != "" {
		t.Fatalf("nil Load() = %q, want %q", s, "")
	}
}

func TestSubOpFromContextWrongType(t *testing.T) {
	type otherKey struct{}
	ctx := context.WithValue(context.Background(), otherKey{}, "not a SubOp")
	got := SubOpFromContext(ctx)
	if got != nil {
		t.Fatalf("SubOpFromContext with wrong-type value = %v, want nil", got)
	}
}
