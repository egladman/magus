package origin_test

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/mcp/origin"
)

func TestOriginRoundTrip(t *testing.T) {
	o := origin.Origin{Agent: "claude-desktop/0.7.2"}
	ctx := origin.WithContext(context.Background(), o)
	got, ok := origin.FromContext(ctx)
	if !ok {
		t.Fatal("FromContext returned ok=false after WithContext")
	}
	if got.Agent != o.Agent {
		t.Errorf("Agent = %q, want %q", got.Agent, o.Agent)
	}
}

func TestOriginFromContext_EmptyContext(t *testing.T) {
	_, ok := origin.FromContext(context.Background())
	if ok {
		t.Error("FromContext on plain context should return ok=false")
	}
}

func TestOriginFromContext_InnerShadowsOuter(t *testing.T) {
	outer := origin.WithContext(context.Background(), origin.Origin{Agent: "outer"})
	inner := origin.WithContext(outer, origin.Origin{Agent: "inner"})
	got, ok := origin.FromContext(inner)
	if !ok || got.Agent != "inner" {
		t.Errorf("got (%v, %v), want (inner, true)", got, ok)
	}
}
