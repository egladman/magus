//go:build !unix && !windows && !js

package vcs

// No Stat_t.Uid here, so the fast path defers to git the way it does for an
// unborn branch.
func pathOwnedByCurrentUser(string) bool { return false }

func pathDevice(string) (uint64, bool) { return 0, false }
