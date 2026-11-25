package types

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// wrapperPrefix starts the id of an invocation that only wraps a command.
const wrapperPrefix = "wrap-"

// WrapperInvocation marks id as an invocation that only wraps a command, as
// `magus job exec --timeout` does: it holds slots and runs no step. Admission
// reads that off the ref: a magus under it can fail no running step.
func WrapperInvocation(id string) string { return wrapperPrefix + id }

// IsWrapperInvocation reports whether ref came from a [WrapperInvocation] id.
func IsWrapperInvocation(ref string) bool {
	_, id, ok := strings.Cut(ref, ":")
	return ok && strings.HasPrefix(id, wrapperPrefix)
}

// RunsUnderAStep reports whether ancestors name an invocation that may be
// running a step blocked on this one: any but a wrapper.
func RunsUnderAStep(ancestors []string) bool {
	return slices.ContainsFunc(ancestors, func(ref string) bool { return !IsWrapperInvocation(ref) })
}

type invocationAncestorsKey struct{}

// WithInvocationAncestors carries the invocations this one is running underneath, oldest
// first, with this invocation itself last. Each entry is an opaque reference minted by
// [AppendInvocationAncestor]; a caller only ever compares them.
//
// It exists so a nested magus can tell a lock its OWN ancestor holds (which can never be
// released, because the ancestor is blocked waiting on this process to exit) from a lock
// an unrelated concurrent magus holds, which will be released shortly. The two look
// identical to flock, and treating the first as the second is what made a nested
// `magus run` hang forever instead of failing.
//
// It is a context value rather than package state because the server runs many
// invocations concurrently in ONE process: a global would report the union of every
// in-flight run's ancestry and refuse work that is merely concurrent.
func WithInvocationAncestors(ctx context.Context, refs []string) context.Context {
	return context.WithValue(ctx, invocationAncestorsKey{}, slices.Clone(refs))
}

// InvocationAncestorsFromContext returns the ancestry WithInvocationAncestors stored,
// oldest first, or nil when none was set. A library caller that never stamps it reads nil,
// which disables re-entry detection and restores the plain waiting behavior.
//
// The result is a copy: the server derives many contexts from one parent, and handing out
// the stored slice would let an append by one invocation land in another's.
func InvocationAncestorsFromContext(ctx context.Context) []string {
	refs, _ := ctx.Value(invocationAncestorsKey{}).([]string)
	return slices.Clone(refs)
}

// InvocationRoot returns the outermost invocation ctx runs underneath, which is this
// invocation itself when nothing started it; "" when ctx carries no ancestry. Invocations
// sharing a root belong to one run: an ancestor and its descendants, or the siblings of
// one fan-out.
func InvocationRoot(ctx context.Context) string {
	refs, _ := ctx.Value(invocationAncestorsKey{}).([]string)
	if len(refs) == 0 {
		return ""
	}
	return refs[0]
}

// AppendInvocationAncestor returns ctx with this invocation appended to the ancestry
// already on it. Called once per invocation, as soon as its id is known.
func AppendInvocationAncestor(ctx context.Context, pid int, id string) context.Context {
	if id == "" {
		return ctx
	}
	return WithInvocationAncestors(ctx, append(InvocationAncestorsFromContext(ctx), fmt.Sprintf("%s:%s", strconv.Itoa(pid), id)))
}

// HasInvocationAncestor reports whether the invocation identified by pid and id is this
// invocation or one of its ancestors.
func HasInvocationAncestor(ctx context.Context, pid int, id string) bool {
	if id == "" {
		return false
	}
	return slices.Contains(InvocationAncestorsFromContext(ctx), fmt.Sprintf("%s:%s", strconv.Itoa(pid), id))
}
