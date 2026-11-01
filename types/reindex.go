package types

import "errors"

// ReindexRefusals is the part of a reindex failure that fails `graph build`: each
// project refused because the run this one is nested inside holds its lock
// (MGS3007), because the machine's build budget could not seat the indexer
// (MGS3009), or because the machine is short of memory (MGS3053). A missing
// indexer is the machine's state and stays a warning. Nil when nothing was
// refused.
func ReindexRefusals(err error) error {
	errs := []error{err}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs = joined.Unwrap()
	}
	var refused []error
	for _, e := range errs {
		if errors.Is(e, ProjectLockHeldByAncestor) || errors.Is(e, MachineBudgetExhausted) || errors.Is(e, MemoryPressureHigh) {
			refused = append(refused, e)
		}
	}
	return errors.Join(refused...)
}
