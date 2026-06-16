package origin

import (
	"context"
	"testing"
)

func TestOriginRoundTrip(t *testing.T) {
	o := Origin{Agent: "claude-desktop/0.7.2"}
	ctx := WithContext(context.Background(), o)
	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("FromContext returned ok=false after WithContext")
	}
	if got.Agent != o.Agent {
		t.Errorf("Agent = %q, want %q", got.Agent, o.Agent)
	}
}

func TestOriginFromContext_EmptyContext(t *testing.T) {
	_, ok := FromContext(context.Background())
	if ok {
		t.Error("FromContext on plain context should return ok=false")
	}
}

func TestOriginFromContext_InnerShadowsOuter(t *testing.T) {
	outer := WithContext(context.Background(), Origin{Agent: "outer"})
	inner := WithContext(outer, Origin{Agent: "inner"})
	got, ok := FromContext(inner)
	if !ok || got.Agent != "inner" {
		t.Errorf("got (%v, %v), want (inner, true)", got, ok)
	}
}
