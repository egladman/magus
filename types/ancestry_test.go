package types

import (
	"context"
	"testing"
)

// The mark rides the ref, so it goes wherever ancestry goes.
func TestAWrapperIsToldApartFromARun(t *testing.T) {
	refs := InvocationAncestorsFromContext(AppendInvocationAncestor(context.Background(), 100, WrapperInvocation("inv-a")))
	if len(refs) != 1 || !IsWrapperInvocation(refs[0]) {
		t.Fatalf("refs = %q, want one wrapper", refs)
	}
	if IsWrapperInvocation("100:inv-a") || IsWrapperInvocation("wrap-a") || IsWrapperInvocation("") {
		t.Fatal("only a minted wrapper ref reads as a wrapper")
	}
	if RunsUnderAStep(nil) || RunsUnderAStep(refs) {
		t.Fatal("nothing above a wrapper can be a blocked step")
	}
	if !RunsUnderAStep(append(refs, "200:inv-b")) {
		t.Fatal("a run under the wrapper can be")
	}
}

// TestInvocationAncestryIdentity pins what the lock's refuse-or-wait decision rests on: an
// invocation is identified by its pid AND its id, because journal ids are only
// process-unique. Two processes that mint the same id must not look like each other's
// ancestor, or a CI fan-out refuses runs that should queue.
func TestInvocationAncestryIdentity(t *testing.T) {
	ctx := AppendInvocationAncestor(context.Background(), 100, "inv-a")
	ctx = AppendInvocationAncestor(ctx, 100, "inv-b")

	if !HasInvocationAncestor(ctx, 100, "inv-a") {
		t.Error("the ancestor this invocation is nested inside must match")
	}
	if !HasInvocationAncestor(ctx, 100, "inv-b") {
		t.Error("this invocation itself must match; the server holds locks under its own id")
	}
	if HasInvocationAncestor(ctx, 200, "inv-a") {
		t.Error("same id from a DIFFERENT pid is a different invocation and must not match")
	}
	if HasInvocationAncestor(ctx, 100, "inv-c") {
		t.Error("an unrelated invocation must not match")
	}
	if HasInvocationAncestor(ctx, 100, "") {
		t.Error("an empty id is unattributable and must never match")
	}
}

// TestInvocationAncestorsDoNotAlias covers the server case the carrier exists for: many
// invocations derive from one context, and an append by one must not be visible to another.
func TestInvocationAncestorsDoNotAlias(t *testing.T) {
	parent := AppendInvocationAncestor(context.Background(), 1, "inv-root")

	a := AppendInvocationAncestor(parent, 1, "inv-a")
	b := AppendInvocationAncestor(parent, 1, "inv-b")

	if HasInvocationAncestor(a, 1, "inv-b") || HasInvocationAncestor(b, 1, "inv-a") {
		t.Fatal("sibling invocations see each other's ids; the appends share a backing array")
	}
	if got := InvocationAncestorsFromContext(parent); len(got) != 1 {
		t.Errorf("parent ancestry = %v, want it unchanged by either child", got)
	}

	// The getter must not hand out the stored slice either, or a caller can rewrite
	// another invocation's identity through it.
	refs := InvocationAncestorsFromContext(a)
	refs[0] = "tampered"
	if !HasInvocationAncestor(a, 1, "inv-root") {
		t.Error("mutating the returned slice changed the ancestry on the context")
	}
}

// TestInvocationAncestorsEmpty pins the disabled state: no ancestry means no re-entry
// detection, which is the pre-existing waiting behavior rather than a refusal.
func TestInvocationAncestorsEmpty(t *testing.T) {
	if got := InvocationAncestorsFromContext(context.Background()); got != nil {
		t.Errorf("ancestry with nothing stamped = %v, want nil", got)
	}
	if HasInvocationAncestor(context.Background(), 1, "inv-a") {
		t.Error("an unstamped context must match nothing")
	}
	if got := AppendInvocationAncestor(context.Background(), 1, ""); got != context.Background() {
		t.Error("appending an empty id must leave the context alone")
	}
}
