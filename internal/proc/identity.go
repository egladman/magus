package proc

import (
	"runtime/debug"

	"github.com/egladman/magus/internal/proc/buildid"
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
// them with the wrong code. Fingerprinting each dev build from its embedded VCS stamp
// makes builds of different revisions (and every dirty build) refuse to adopt each other.

// devVersionSentinel is the placeholder main.version carries in an unstamped dev build:
// `go build`/`go run ./cmd/magus` without `-ldflags "-X main.version=..."`. Every RELEASE
// build is stamped with a real version (git describe) by the magusfile, so any value
// other than this exact string is a stamped release and passes the gate on its version as
// before. Keep in sync with the default of `version` in cmd/magus/version.go.
const devVersionSentinel = "unknown"

// adoptionIdentity maps a build's DISPLAY version to the identity used for the server
// adoption gate. Client and server both run it over their own build, so two builds adopt
// each other only when their identities are equal. It is total and deterministic per
// process:
//
//   - "" passes through unchanged. An empty identity on either side disables the gate; it
//     is the test-injection / pre-versioning escape hatch (see service.versionAdmits).
//   - A MODIFIED-tree build maps to [buildid.File] whatever its display version says. A
//     stamped `git describe --dirty` string is shared by every dirty build of one commit,
//     so trusting it let a stale server adopt a since-rebuilt client and execute the run
//     with old code; the executable-file identity admits only the byte-identical binary.
//   - A stamped release version (anything but devVersionSentinel) passes through unchanged,
//     so releases keep matching on their exact version.
//   - The dev sentinel is replaced by a build fingerprint: "dev-<revision>" for a CLEAN
//     build carrying VCS info (two clean builds of one commit are provably the same code
//     and DO adopt each other, preserving the run-a-server-then-adopt-into-it workflow),
//     or the per-process [buildid.Unverifiable] token for a VCS-less build (never matches:
//     adoption refused).
//
// Embedding the fingerprint IN the version string (rather than adding a new wire field) is
// deliberate and load-bearing: a stale PRE-FIX server compares the string it receives
// against its own stored "unknown" and correctly mismatches, refusing the call. A new wire
// field would be silently ignored by that old server and fail OPEN: the very failure mode
// this fix exists to close.
func adoptionIdentity(displayVersion string) string {
	if displayVersion == "" {
		return "" // the check-disabled / test-injection escape hatch stays open
	}
	rev, modified, ok := buildVCS()
	if ok && modified {
		return buildid.File()
	}
	if displayVersion != devVersionSentinel {
		return displayVersion
	}
	if !ok || rev == "" {
		return buildid.Unverifiable()
	}
	return "dev-" + rev
}

// buildVCS reads the VCS stamp the Go toolchain embeds in the binary at build time (Go
// 1.18+). ok is false only when no build info is available at all (e.g. a binary built
// with -ldflags that strips it in an unusual way); rev and modified are the vcs.revision
// and vcs.modified settings, which are absent ("" / false) when the build carried no VCS
// info at all: built with -buildvcs=false, or outside a VCS work tree. A var so tests
// can pin the build state instead of inheriting whatever tree built the test binary.
var buildVCS = func() (rev string, modified bool, ok bool) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false, false
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return rev, modified, true
}
