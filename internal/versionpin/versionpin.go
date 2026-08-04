// Package versionpin compares the running magus binary's version against a
// workspace's declared minimum (magus.yaml's requires_magus). It is used both at
// workspace load (magus.go warns) and by `magus doctor` (a named check), so the
// comparison logic - and its "nothing to report" cases - live in exactly one place.
package versionpin

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/egladman/magus/types"
)

// Check compares running (the executing binary's stamped version, e.g.
// types.MagusVersionFromContext) against required (a workspace's requires_magus
// config value: a plain semver minimum such as "0.4.0"; a leading "v" is optional).
//
// It returns "" when there is nothing to report: required is unset, required is
// not a valid semver string, running is a dev build (no reliable version to
// compare - see types.IsDevMagusVersion), or running already satisfies the
// minimum. Otherwise it returns a message naming both versions, for the caller to
// wrap at whatever severity fits its surface (a load-time warn, a doctor check).
func Check(running, required string) string {
	if required == "" {
		return ""
	}
	if types.IsDevMagusVersion(running) {
		return ""
	}
	req := withV(required)
	if !semver.IsValid(req) {
		return ""
	}
	run := withV(running)
	if !semver.IsValid(run) {
		return ""
	}
	if semver.Compare(run, req) >= 0 {
		return ""
	}
	return fmt.Sprintf("running magus %s is older than this workspace requires (requires_magus: %s)", running, required)
}

// withV prefixes v with "v" when absent, since golang.org/x/mod/semver requires
// the leading "v" that magus.yaml's requires_magus does not.
func withV(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}
