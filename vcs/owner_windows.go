//go:build windows

package vcs

// git's ownership check on Windows compares SIDs and ACLs, which this fast path does not
// read: it reports every path owned, and a repository git would refuse is not caught here.
func pathOwnedByCurrentUser(string) bool { return true }

// pathDevice reports no device, so discovery never stops at a volume boundary. A walk up
// a Windows path stays on one drive anyway.
func pathDevice(string) (uint64, bool) { return 0, false }
