package proc

import (
	"testing"

	"github.com/egladman/magus/internal/ward"
	"github.com/stretchr/testify/assert"
)

// devVersionSentinel's doc says to keep it in sync with ward.DevVersion (and cmd/magus's
// unknownVersion, checked by TestUnknownVersionMatchesWardDevVersion in cmd/magus). Neither
// half of that pair could see the other without this test: cmd/magus cannot see proc's
// unexported sentinel, so each package only proves the half reachable from ward.DevVersion.
func TestDevVersionSentinelMatchesWard(t *testing.T) {
	assert.Equal(t, ward.DevVersion, devVersionSentinel)
}

// pinBuildVCS forces the build state adoptionIdentity reads, restoring the real
// reader on cleanup, so these tests do not inherit the VCS state of whatever tree
// built the test binary.
func pinBuildVCS(t *testing.T, rev string, modified, ok bool) {
	t.Helper()
	restore := buildVCS
	buildVCS = func() (string, bool, bool) { return rev, modified, ok }
	t.Cleanup(func() { buildVCS = restore })
}

// adoptionIdentity is symmetric and total: "" and stamped releases pass through, only the
// dev sentinel is rewritten to a fingerprint. These cases pin the pass-through halves for
// a CLEAN build; a modified build never passes through (see
// TestAdoptionIdentityStampedDirtyIsNotShared).
func TestAdoptionIdentityPassThrough(t *testing.T) {
	pinBuildVCS(t, "abc123", false, true)
	cases := map[string]string{
		"":                 "",       // the check-disabled / test-injection escape hatch
		"v0.4.2":           "v0.4.2", // a stamped release version
		"v0.4.2-3-gabc123": "v0.4.2-3-gabc123",
		"test":             "test", // the literal used by existing round-trip tests
	}
	for in, want := range cases {
		assert.Equalf(t, want, adoptionIdentity(in), "adoptionIdentity(%q)", in)
	}
}

// A stamped dirty build's display version is `git describe --dirty`, which every dirty
// build of one commit shares - the daemon built at one dirty state and a client rebuilt
// at another carry the SAME string while running different code. Passing it through let
// the stale daemon adopt the newer client and execute the run with old code. The
// identity must therefore be the executable file's, never the shared string.
func TestAdoptionIdentityStampedDirtyIsNotShared(t *testing.T) {
	pinBuildVCS(t, "6bfd32710", true, true)
	const stamped = "v0.4.2-2-g6bfd32710-dirty"
	got := adoptionIdentity(stamped)
	assert.NotEqual(t, stamped, got, "a dirty build must not gate adoption on its shared describe string")
	assert.Equal(t, binaryIdentity, got, "a dirty build identifies as the executable file it runs from")
	assert.NotEmpty(t, got)

	// The escape hatch outranks the dirty rule: "" still disables the gate.
	assert.Equal(t, "", adoptionIdentity(""))
}

// binaryIdentity is never empty (an empty identity would disable the version gate) and
// maps the same executable file to the same value, so a process and the children it
// forks from that file keep adopting each other.
func TestBinaryIdentityStableAndNonEmpty(t *testing.T) {
	assert.NotEmpty(t, binaryIdentity)
	if binaryIdentity != devUnverifiable {
		assert.Contains(t, binaryIdentity, "dev-bin-")
		assert.Equal(t, binaryIdentity, computeBinaryIdentity(), "the same executable file must map to the same identity")
	}
}

// A dev build's identity is never the raw sentinel: it is either "dev-<revision>" (clean)
// or the per-process devUnverifiable token (dirty / no VCS). Whichever this test binary is,
// the result must differ from "unknown" so two dev builds cannot match on the placeholder.
func TestAdoptionIdentityDevIsFingerprinted(t *testing.T) {
	got := adoptionIdentity(devVersionSentinel)
	assert.NotEqual(t, devVersionSentinel, got, "a dev build must not keep the shared placeholder as its identity")
	assert.NotEmpty(t, got, "a dev build's identity is never empty (that would disable the gate)")

	// Mirror adoptionIdentity's own decision (go test binaries typically carry no
	// vcs.revision, so the unverifiable branch is the common one under `go test`).
	rev, modified, ok := buildVCS()
	switch {
	case ok && modified:
		// Dirty build: the identity is the executable file's, per-file not per-process.
		assert.Equal(t, binaryIdentity, got)
	case !ok || rev == "":
		// Unprovable build: identity is the per-process token, which never matches anything.
		assert.Equal(t, devUnverifiable, got)
	default:
		// Clean build with an embedded revision: identity encodes it and is stable per build.
		assert.Equal(t, "dev-"+rev, got)
		assert.NotEqual(t, devUnverifiable, got, "a clean build has a revision-derived, not per-process, identity")
		assert.Equal(t, got, adoptionIdentity(devVersionSentinel), "a clean build's identity is deterministic")
	}
}

// devUnverifiable must be a per-process-unique token so two unprovable builds never adopt
// each other. Regenerating it must not reproduce the same value.
func TestDevUnverifiableIsUnique(t *testing.T) {
	assert.NotEqual(t, "dev-unverifiable-"+randomToken(), devUnverifiable, "the unverifiable token must be freshly random per process")
	assert.NotEqual(t, randomToken(), randomToken(), "randomToken must not repeat")
}

// versionAdmits is the shared gate. It admits when either side is empty (check disabled) or
// the two identities match exactly, and refuses otherwise. Asserting it directly documents
// the full truth table independent of the wire round-trip.
func TestVersionAdmits(t *testing.T) {
	cases := []struct {
		name       string
		gate, req  string
		wantAdmits bool
	}{
		{"both empty passes (test escape)", "", "", true},
		{"empty gate disables the check", "", "dev-abc", true},
		{"empty request passes (pre-versioning client)", "dev-abc", "", true},
		{"same identity adopts", "dev-abc", "dev-abc", true},
		{"different dev revisions refuse", "dev-abc", "dev-def", false},
		{"release vs different release refuses", "v0.4.2", "v0.4.1", false},
		{"same release adopts", "v0.4.2", "v0.4.2", true},
		{"release vs dev refuses", "v0.4.2", "dev-abc", false},
	}
	for _, tc := range cases {
		s := &service{gateVersion: tc.gate}
		assert.Equalf(t, tc.wantAdmits, s.versionAdmits(tc.req), "%s", tc.name)
	}
}
