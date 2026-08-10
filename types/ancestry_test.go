package types_test

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
)

// TestInvocationAncestryIdentity pins what the lock's refuse-or-wait decision rests on: an
// invocation is identified by its pid AND its id, because journal ids are only
// process-unique. Two processes that mint the same id must not look like each other's
// ancestor, or a CI fan-out refuses runs that should queue.
func TestInvocationAncestryIdentity(t *testing.T) {
	ctx := types.AppendInvocationAncestor(context.Background(), 100, "inv-a")
	ctx = types.AppendInvocationAncestor(ctx, 100, "inv-b")

	if !types.HasInvocationAncestor(ctx, 100, "inv-a") {
		t.Error("the ancestor this invocation is nested inside must match")
	}
	if !types.HasInvocationAncestor(ctx, 100, "inv-b") {
		t.Error("this invocation itself must match; the daemon holds locks under its own id")
	}
	if types.HasInvocationAncestor(ctx, 200, "inv-a") {
		t.Error("same id from a DIFFERENT pid is a different invocation and must not match")
	}
	if types.HasInvocationAncestor(ctx, 100, "inv-c") {
		t.Error("an unrelated invocation must not match")
	}
	if types.HasInvocationAncestor(ctx, 100, "") {
		t.Error("an empty id is unattributable and must never match")
	}
}

// TestInvocationAncestorsDoNotAlias covers the daemon case the carrier exists for: many
// invocations derive from one context, and an append by one must not be visible to another.
func TestInvocationAncestorsDoNotAlias(t *testing.T) {
	parent := types.AppendInvocationAncestor(context.Background(), 1, "inv-root")

	a := types.AppendInvocationAncestor(parent, 1, "inv-a")
	b := types.AppendInvocationAncestor(parent, 1, "inv-b")

	if types.HasInvocationAncestor(a, 1, "inv-b") || types.HasInvocationAncestor(b, 1, "inv-a") {
		t.Fatal("sibling invocations see each other's ids; the appends share a backing array")
	}
	if got := types.InvocationAncestorsFromContext(parent); len(got) != 1 {
		t.Errorf("parent ancestry = %v, want it unchanged by either child", got)
	}

	// The getter must not hand out the stored slice either, or a caller can rewrite
	// another invocation's identity through it.
	refs := types.InvocationAncestorsFromContext(a)
	refs[0] = "tampered"
	if !types.HasInvocationAncestor(a, 1, "inv-root") {
		t.Error("mutating the returned slice changed the ancestry on the context")
	}
}

// TestInvocationAncestorsEmpty pins the disabled state: no ancestry means no re-entry
// detection, which is the pre-existing waiting behavior rather than a refusal.
func TestInvocationAncestorsEmpty(t *testing.T) {
	if got := types.InvocationAncestorsFromContext(context.Background()); got != nil {
		t.Errorf("ancestry with nothing stamped = %v, want nil", got)
	}
	if types.HasInvocationAncestor(context.Background(), 1, "inv-a") {
		t.Error("an unstamped context must match nothing")
	}
	if got := types.AppendInvocationAncestor(context.Background(), 1, ""); got != context.Background() {
		t.Error("appending an empty id must leave the context alone")
	}
}
