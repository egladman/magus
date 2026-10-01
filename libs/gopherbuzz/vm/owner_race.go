//go:build race

package vm

// Under the race detector a released slot is poisoned rather than recycled, as
// sync.Pool drops instead of reusing there: a use after Close then panics where
// a recycled slot would quietly read as some later object.
func init() { poisonReleased = true }
