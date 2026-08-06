package ward

import (
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/egladman/magus/types"
)

// RequiredVersion checks the running build against the workspace's declared floor
// (magus.yaml required_version) and returns MGS1021 when the build is too old.
//
// This is the only ward an OLD binary can enforce, and that asymmetry is the whole
// reason it exists. When a magusfile reaches for a host module or a project key
// added after the running binary was built, the binary that reports the failure is
// the old one - it cannot look up which release introduced the thing it does not
// have, because it has never heard of that release. The failure therefore surfaces
// wherever the magusfile happened to touch it first (`import "xml": module not
// found`), which reads like a typo rather than an out-of-date tool. A declared
// floor is a claim the old binary CAN evaluate, so it must be checked before the
// magusfile is evaluated at all.
//
// Both arguments tolerate being absent, in opposite directions and for different
// reasons:
//
//   - constraint == "" is a workspace that declares no floor. Most workspaces, and
//     every one written before this existed.
//   - running == "" is a caller that never supplied a version: a bare library
//     caller of magus.Open, or a test. It has no version to be too old, so there is
//     nothing to compare and the check passes. Same escape hatch the daemon
//     adoption gate uses.
//
// A DEV BUILD also passes, in either spelling: the unstamped sentinel ("unknown") and
// the git-describe form a source build carries ("v0.3.0-286-gabc123"). Both are newer
// than the release they name, and blocking one against a floor the working tree itself
// just raised is exactly backwards.
//
// The describe form used to be compared, and that made a floor for an UNRELEASED feature
// unarmable. `git describe` says "286 commits past v0.3.0", but semver parses that as the
// prerelease 0.3.0-286-gabc123, which orders BELOW 0.3.0 - the opposite of what the
// string means. Stripping the prerelease recovers 0.3.0, so a floor naming the release
// that will first carry the feature rejected every build made before that release was
// tagged, including the source-path builds CI uses to exercise a magusfile change at the
// commit that introduces it. The floor could only be raised AFTER the release, which is
// precisely when it is no longer the thing anyone needed protecting from.
//
// Exempting it is not a hole. A source build is compiled FROM the workspace it then runs,
// so it cannot lack a feature that workspace's magusfile uses - they are the same commit.
// The binary a floor exists to catch is one built somewhere else and shipped: a release.
// types.IsDevMagusVersion is the same test the drift classifier uses, so "what counts as
// a dev build" has one answer.
//
// A malformed constraint is an error rather than a silent pass. A floor nobody can
// parse protects nobody, and failing loudly on it is what keeps a typo from
// reading as "no floor declared".
func RequiredVersion(constraint, running string) *types.DiagnosticError {
	if constraint == "" || running == "" || types.IsDevMagusVersion(running) {
		return nil
	}
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus,
			"magus.yaml required_version %q is not a valid semver constraint: %v. "+
				"Use a comparison like \">= 0.4.0\", or remove the key to declare no floor.",
			constraint, err)
	}
	v, err := semver.NewVersion(strings.TrimPrefix(running, "v"))
	if err != nil {
		// A running version this ward cannot parse is not the workspace's fault, and
		// refusing to load over it would strand the user with no way forward. Nothing
		// to compare, so nothing to report.
		//nolint:nilerr // an unparsable running version means no comparison to make, not a failure to report
		return nil
	}
	// Compare on the release alone. A prerelease of the version that satisfies the
	// floor is treated as satisfying it, because semver constraints otherwise
	// exclude every prerelease - so a v0.4.0-rc1 binary would be told to upgrade to
	// 0.4.0, which it effectively already is.
	base, err := v.SetPrerelease("")
	if err == nil {
		v = &base
	}
	if c.Check(v) {
		return nil
	}
	return types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus,
		"this workspace requires magus %s and this build is %s. "+
			"Upgrade the binary (`magus self update`), or if this is CI, raise the pinned "+
			"version in your magus setup step. The workspace declares the floor in "+
			"magus.yaml (required_version).",
		constraint, running)
}

// DevVersion is the placeholder an unstamped dev build carries. It mirrors the
// default of `version` in cmd/magus/version.go (and proc's devVersionSentinel);
// keep the three in sync.
const DevVersion = "unknown"
