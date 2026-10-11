package cache

import "errors"

// needFailure marks an error as the failure of one ctx.needs member of a composed target,
// and carries the member's target and the ref its own run recorded. Error is the wrapped
// text unchanged, so the cause parsing in hopChain and causeSignature, which reads that
// text, keeps working.
type needFailure struct {
	target string
	ref    string
	err    error
}

func (e *needFailure) Error() string { return e.err.Error() }
func (e *needFailure) Unwrap() error { return e.err }

// WrapNeedFailure marks err as the failure of the ctx.needs member target, whose own run
// recorded ref, unless err already carries a member's failure from deeper in the
// composition, which is the one worth reading. It returns err untouched when ref is empty
// (the member's output was not persisted) or err is nil.
func WrapNeedFailure(err error, target, ref string) error {
	if err == nil || ref == "" {
		return err
	}
	var nf *needFailure
	if errors.As(err, &nf) {
		return err
	}
	return &needFailure{target: target, ref: ref, err: err}
}

// asNeedFailure returns the target and ref of the ctx.needs member err failed on, or empty
// strings when err is not a composed target's failure.
func asNeedFailure(err error) (target, ref string) {
	var nf *needFailure
	if errors.As(err, &nf) {
		return nf.target, nf.ref
	}
	return "", ""
}
