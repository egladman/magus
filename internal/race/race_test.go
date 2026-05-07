package race_test

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/race"
)

func TestNewRuntime_NonNil(t *testing.T) {
	rt := race.NewRuntime(t.TempDir())
	if rt == nil {
		t.Fatal("NewRuntime returned nil")
	}
}

func TestRuntimeContext_RoundTrip(t *testing.T) {
	rt := race.NewRuntime(t.TempDir())
	ctx := race.WithRuntime(context.Background(), rt)
	got := race.RuntimeFromContext(ctx)
	if got != rt {
		t.Error("RuntimeFromContext returned different Runtime than stored")
	}
}

func TestRuntimeFromContext_Empty(t *testing.T) {
	if race.RuntimeFromContext(context.Background()) != nil {
		t.Error("RuntimeFromContext(empty context) should return nil")
	}
}
