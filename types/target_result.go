package types

import "time"

// TargetStatus is how one target of a run ended.
type TargetStatus string

const (
	// TargetOK ran, or was replayed from the cache, and passed.
	TargetOK TargetStatus = "ok"
	// TargetPermitted passed only because a deviation ledger allowed its
	// findings.
	TargetPermitted TargetStatus = "permitted"
	// TargetFailed ran and failed; TargetResult.Err says why.
	TargetFailed TargetStatus = "failed"
)

// TargetResult is the outcome of one target of a run, handed to the
// function WithTargetResults names. A cache replay reports one too.
//
// A JSONL sink's run.target.result record carries the same facts as text.
// Here Err stays the error itself, so a caller matches it with errors.As.
type TargetResult struct {
	Project  string
	Target   string
	Status   TargetStatus
	CacheHit bool
	// Hash is the cache key the target ran under.
	Hash     string
	Duration time.Duration
	// Ref names the target's stored output; Magus.OutputByRef reads it.
	Ref string
	// Err is nil unless Status is TargetFailed.
	Err error
	// Next is a failure's remedy, the steps the CLI prints under it.
	Next []NextStep
}
