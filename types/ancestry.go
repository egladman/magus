package types

import (
	"context"
	"slices"
	"strconv"
)

type invocationAncestorsKey struct{}

// WithInvocationAncestors carries the invocations this one is running underneath, oldest
// first, with this invocation itself last. Each entry is an opaque reference minted by
// [AppendInvocationAncestor]; a caller only ever compares them.
//
// It exists so a nested magus can tell a lock its OWN ancestor holds - which can never be
// released, because the ancestor is blocked waiting on this process to exit - from a lock
// an unrelated concurrent magus holds, which will be released shortly. The two look
// identical to flock, and treating the first as the second is what made a nested
// `magus run` hang forever instead of failing.
//
// It is a context value rather than package state because the daemon runs many
// invocations concurrently in ONE process: a global would report the union of every
// in-flight run's ancestry and refuse work that is merely concurrent.
func WithInvocationAncestors(ctx context.Context, refs []string) context.Context {
	return context.WithValue(ctx, invocationAncestorsKey{}, slices.Clone(refs))
}

// InvocationAncestorsFromContext returns the ancestry WithInvocationAncestors stored,
// oldest first, or nil when none was set. A library caller that never stamps it reads nil,
// which disables re-entry detection and restores the plain waiting behavior.
//
// The result is a copy: the daemon derives many contexts from one parent, and handing out
// the stored slice would let an append by one invocation land in another's.
func InvocationAncestorsFromContext(ctx context.Context) []string {
	refs, _ := ctx.Value(invocationAncestorsKey{}).([]string)
	return slices.Clone(refs)
}

// AppendInvocationAncestor returns ctx with this invocation appended to the ancestry
// already on it. Called once per invocation, as soon as its id is known.
func AppendInvocationAncestor(ctx context.Context, pid int, id string) context.Context {
	if id == "" {
		return ctx
	}
	return WithInvocationAncestors(ctx, append(InvocationAncestorsFromContext(ctx), invocationRef(pid, id)))
}

// HasInvocationAncestor reports whether the invocation identified by pid and id is this
// invocation or one of its ancestors.
func HasInvocationAncestor(ctx context.Context, pid int, id string) bool {
	if id == "" {
		return false
	}
	return slices.Contains(InvocationAncestorsFromContext(ctx), invocationRef(pid, id))
}

// invocationRef is the identity two invocations are compared by: the pid that minted the
// id, plus the id.
//
// The pid is not decoration. journal.NewInvocationID is documented as PROCESS-unique - it
// is a millisecond stamp plus a per-process counter - so two magus processes that mint
// their first id in the same millisecond mint the same string. On the id alone, a CI
// fan-out that starts several runs at once would have one refuse another as its own
// ancestor. A pid is unique among live processes, so the pair is unique among live
// invocations, which is all this comparison spans. Under the daemon the pid is shared by
// holder and waiter and the ids differ, which is the case the ids alone handle.
func invocationRef(pid int, id string) string {
	return strconv.Itoa(pid) + ":" + id
}
