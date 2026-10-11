package cache

import "errors"

// NeedFailure marks an error as the failure of one ctx.needs member of a composed target, and
// carries the member's target and the ref its own run recorded. Error is the wrapped text
// unchanged, so the cause parsing in hopChain and causeSignature, which reads that text,
// keeps working.
type NeedFailure struct {
	Target string
	Ref    string
	Err    error
}

func (e *NeedFailure) Error() string { return e.Err.Error() }
func (e *NeedFailure) Unwrap() error { return e.Err }

// WrapNeedFailure marks err as member's failure, unless err already carries a NeedFailure
// from deeper in the composition, which is the one worth reading. It returns err untouched
// when ref is empty (the member's output was not persisted) or err is nil.
func WrapNeedFailure(err error, target, ref string) error {
	if err == nil || ref == "" {
		return err
	}
	var nf *NeedFailure
	if errors.As(err, &nf) {
		return err
	}
	return &NeedFailure{Target: target, Ref: ref, Err: err}
}

// failedNeed returns the target and ref of the ctx.needs member err failed on, or empty
// strings when err is not a composed target's failure.
func failedNeed(err error) (target, ref string) {
	var nf *NeedFailure
	if errors.As(err, &nf) {
		return nf.Target, nf.Ref
	}
	return "", ""
}
