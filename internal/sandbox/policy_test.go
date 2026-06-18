package sandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPolicyContext_RoundTrip(t *testing.T) {
	p := &Policy{}
	ctx := WithPolicy(context.Background(), p)
	got := FromContext(ctx)
	assert.Same(t, p, got, "FromContext should return the stored Policy")
}

func TestFromContext_Empty(t *testing.T) {
	assert.Nil(t, FromContext(context.Background()), "FromContext(empty) should return nil")
}

func TestPolicy_CheckRead_NilPolicy(t *testing.T) {
	var p *Policy
	// A nil policy (no restrictions) should allow all reads.
	assert.NoError(t, p.CheckRead("/any/path"), "nil Policy.CheckRead should allow all reads")
}

func TestUnionPolicies_NilSafe(t *testing.T) {
	p := UnionPolicies()
	assert.NotNil(t, p, "UnionPolicies() of zero policies should not return nil")
}
