package sandbox_test

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/sandbox"
)

func TestPolicyContext_RoundTrip(t *testing.T) {
	p := &sandbox.Policy{}
	ctx := sandbox.WithPolicy(context.Background(), p)
	got := sandbox.FromContext(ctx)
	if got != p {
		t.Error("FromContext returned different Policy than stored")
	}
}

func TestFromContext_Empty(t *testing.T) {
	if sandbox.FromContext(context.Background()) != nil {
		t.Error("FromContext(empty) should return nil")
	}
}

func TestPolicy_CheckRead_NilPolicy(t *testing.T) {
	var p *sandbox.Policy
	// A nil policy (no restrictions) should allow all reads.
	if err := p.CheckRead("/any/path"); err != nil {
		t.Errorf("nil Policy.CheckRead: expected nil error, got %v", err)
	}
}

func TestUnionPolicies_NilSafe(t *testing.T) {
	p := sandbox.UnionPolicies()
	if p == nil {
		t.Error("UnionPolicies() of zero policies returned nil")
	}
}
