package bindings

import "sync"

// reviewProviderSpell names the spell a magusfile selected via magus\review.provider(<spell
// handle>). Empty means none, which is the ordinary state: a workspace that never wires one
// reviews locally and publishes nowhere, and every surface behaves exactly as it did before
// this contract existed.
//
// It lives in the bindings layer for the reason ci_provider.go gives about itself: running the
// spell's contract functions needs the Buzz VM, and core must be able to ask which provider is
// selected without linking it.
var (
	reviewProviderMu   sync.RWMutex
	reviewProviderName string
)

// SetReviewProvider records the spell a magusfile selected as its review backend.
func SetReviewProvider(name string) {
	reviewProviderMu.Lock()
	defer reviewProviderMu.Unlock()
	reviewProviderName = name
}

// ReviewProvider reports the selected spell, empty when a magusfile wired none.
//
// The empty case is answered here rather than left to each caller, because the difference
// between "no provider" and "provider that failed" is what decides whether a surface shows an
// error or simply says nothing. Publishing with no provider is not a failure to report - it is
// a workspace that never asked for one.
func ReviewProvider() string {
	reviewProviderMu.RLock()
	defer reviewProviderMu.RUnlock()
	return reviewProviderName
}
