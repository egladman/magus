// Package pid answers whether a recorded process is still running. One implementation
// with two readers: the cache's in-flight registry, which reports a killed run to the
// next one, and machine-wide admission, which retires the claim a dead run left behind.
//
// Untagged, so the package comment belongs to the package on every platform. Put on the
// !windows file it would have been invisible to a Windows reader and to any tool that
// builds for one.
package pid
