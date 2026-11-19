package proc

import (
	"github.com/egladman/magus/internal/buildinfo"
	"github.com/egladman/magus/types"
)

// This file computes the ADOPTION IDENTITY: the version string a magus build sends over
// the wire and the server compares against to decide whether to adopt a forwarded call.
// It is distinct from the human-facing DISPLAY version (`magus --version`, the server's
// StatusReply.Version): display stays friendly, only the gate uses the fingerprint.
//
// The problem it solves: an unstamped dev build carries a fixed placeholder version
// (devVersionSentinel), so the server gate (a plain string equality) treats EVERY dev
// build as identical to every other. A stale dev server left running from an old, since-
// deleted binary would then adopt runs from an unrelated newer dev client and execute
// them with the wrong code. Fingerprinting each dev build from its build commit makes
// builds of different revisions (and every dirty build) refuse to adopt each other.

// devVersionSentinel is the placeholder buildinfo's version carries in an unstamped dev
// build: `go build`/`go run ./cmd/magus` without `-ldflags "-X .../buildinfo.version=..."`.
// Every RELEASE build is stamped with a real version (git describe) by the magusfile, so
// any value other than this exact string is a stamped release and passes the gate on its
// version as before. Keep in sync with buildinfo.UnknownVersion.
const devVersionSentinel = "unknown"

// adoptionIdentity is [gateIdentity] of displayVersion for this binary's build,
// [buildinfo.Self].
func adoptionIdentity(displayVersion string) string {
	return gateIdentity(displayVersion, buildinfo.Self())
}

// gateIdentity maps a build's DISPLAY version to the identity used for the server
// adoption gate. It judges the build b by its linker stamp first, and by Go's VCS stamp
// only when no stamp exists ([buildinfo.Self]). A build in a linked git worktree records
// another revision and a modified tree in Go's VCS stamp, which would give a clean
// stamped build a per-file identity. Client and server both run it over their own build,
// so two builds adopt each other only when their identities are equal. It is total and
// deterministic per process:
//
//   - "" passes through unchanged. An empty identity on either side disables the gate; it
//     is the test-injection / pre-versioning escape hatch (see service.versionAdmits).
//   - A MODIFIED-tree build maps to [buildinfo.File] whatever its display version says. A
//     stamped `git describe --dirty` string is shared by every dirty build of one commit,
//     so trusting it let a stale server adopt a since-rebuilt client and execute the run
//     with old code; the executable-file identity admits only the byte-identical binary.
//   - A stamped release version (anything but devVersionSentinel) passes through unchanged,
//     so releases keep matching on their exact version.
//   - The dev sentinel is replaced by a build fingerprint. A CLEAN build whose commit is
//     known, from the stamp or else Go's VCS stamp, maps to "dev-<commit>". Two clean
//     builds of one commit are the same code and adopt each other, which keeps the
//     run-a-server-then-adopt-into-it workflow. A build with no identity maps to the
//     per-process [buildinfo.Unverifiable] token, which never matches.
//
// Embedding the fingerprint IN the version string (rather than adding a new wire field) is
// deliberate and load-bearing: a stale PRE-FIX server compares the string it receives
// against its own stored "unknown" and correctly mismatches, refusing the call. A new wire
// field would be silently ignored by that old server and fail OPEN: the very failure mode
// this fix exists to close.
func gateIdentity(displayVersion string, b types.MagusBuild) string {
	switch {
	case displayVersion == "":
		return ""
	case b.Dirty:
		return buildinfo.File()
	case displayVersion != devVersionSentinel:
		return displayVersion
	case b.Source == types.BuildSourceNone || b.Commit == "" || b.Commit == buildinfo.UnknownVersion:
		return buildinfo.Unverifiable()
	}
	return "dev-" + b.Commit
}
