package cache

import (
	"errors"
	"slices"

	"github.com/egladman/magus/types"
)

// needFailure marks an error as the failure of one ctx.needs member.
// Error is the wrapped text unchanged: hopChain and causeSignature parse it.
type needFailure struct {
	project string
	target  string
	ref     string
	err     error
}

func (e *needFailure) Error() string { return e.err.Error() }
func (e *needFailure) Unwrap() error { return e.err }

// NeedMember names a ctx.needs member of a composed target. Ref is the
// output its own run recorded, empty for a member that ran inline.
type NeedMember struct {
	Project string
	Target  string
	Ref     string
}

// WrapNeedFailure marks err as the failure of the ctx.needs member m. A
// member wrapped deeper stays inside, so the chain holds every member the
// failure passed through. err is returned untouched when it is nil or
// m.Target is empty.
func WrapNeedFailure(err error, m NeedMember) error {
	if err == nil || m.Target == "" {
		return err
	}
	return &needFailure{project: m.Project, target: m.Target, ref: m.Ref, err: err}
}

// asNeedFailure returns the deepest member err failed on that has a ref.
// An inline member has no log to read, so its composer is named instead.
func asNeedFailure(err error) (target, ref string) {
	var nf *needFailure
	for errors.As(err, &nf) {
		if nf.ref != "" {
			target, ref = nf.target, nf.ref
		}
		err = nf.err
	}
	return target, ref
}

// FailedNeeds returns the innermost ctx.needs member on each branch of err,
// in the error's order and without duplicates. One ctx.needs(a, b) joins
// the failures of both, so one error can name several members. An empty
// result means the target that returned err failed on its own.
func FailedNeeds(err error) []types.TargetRef {
	var out []types.TargetRef
	for _, ref := range innermostNeeds(err) {
		if !slices.Contains(out, ref) {
			out = append(out, ref)
		}
	}
	return out
}

// namedNeed is a link that names a member. It is asserted one link at a
// time: errors.As would skip to the first member and past the deepest.
type namedNeed interface{ need() types.TargetRef }

func (e *needFailure) need() types.TargetRef {
	return types.TargetRef{Project: e.project, Target: e.target}
}

func innermostNeeds(err error) []types.TargetRef {
	var deepest []types.TargetRef
	for e := err; e != nil; e = errors.Unwrap(e) {
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			var branches []types.TargetRef
			for _, branch := range joined.Unwrap() {
				branches = append(branches, innermostNeeds(branch)...)
			}
			if len(branches) > 0 {
				return branches
			}
			break
		}
		if n, ok := e.(namedNeed); ok {
			deepest = []types.TargetRef{n.need()}
		}
	}
	return deepest
}
