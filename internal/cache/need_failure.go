package cache

import "errors"

// needFailure marks an error as the failure of one ctx.needs member, with
// the member's target and the ref its own run recorded. Error is the wrapped
// text unchanged: hopChain and causeSignature parse it.
type needFailure struct {
	target string
	ref    string
	err    error
}

func (e *needFailure) Error() string { return e.err.Error() }
func (e *needFailure) Unwrap() error { return e.err }

// NeedMember names a ctx.needs member of a composed target and the ref its
// own run recorded.
type NeedMember struct {
	Target string
	Ref    string
}

// WrapNeedFailure marks err as the failure of the ctx.needs member m. An err
// already carrying a deeper member's failure is returned as is: that member
// is the one worth reading. So is a nil err, or one whose member's output
// was not persisted (m.Ref empty).
func WrapNeedFailure(err error, m NeedMember) error {
	if err == nil || m.Ref == "" {
		return err
	}
	var nf *needFailure
	if errors.As(err, &nf) {
		return err
	}
	return &needFailure{target: m.Target, ref: m.Ref, err: err}
}

// asNeedFailure returns the target and ref of the member err failed on, or
// empty strings when err is not a composed target's failure.
func asNeedFailure(err error) (target, ref string) {
	var nf *needFailure
	if errors.As(err, &nf) {
		return nf.target, nf.ref
	}
	return "", ""
}
