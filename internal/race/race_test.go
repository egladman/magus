package race

import (
	"context"
	"testing"
)

func TestNewRuntime_NonNil(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	if rt == nil {
		t.Fatal("NewRuntime returned nil")
	}
}

func TestRuntimeContext_RoundTrip(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	ctx := WithRuntime(context.Background(), rt)
	got := RuntimeFromContext(ctx)
	if got != rt {
		t.Error("RuntimeFromContext returned different Runtime than stored")
	}
}

func TestRuntimeFromContext_Empty(t *testing.T) {
	if RuntimeFromContext(context.Background()) != nil {
		t.Error("RuntimeFromContext(empty context) should return nil")
	}
}
