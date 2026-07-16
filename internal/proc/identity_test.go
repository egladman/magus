package proc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// adoptionIdentity is symmetric and total: "" and stamped releases pass through, only the
// dev sentinel is rewritten to a fingerprint. These cases pin the pass-through halves; the
// fingerprint half depends on this test binary's own VCS stamp, so it is asserted by
// property (see TestAdoptionIdentityDevIsFingerprinted) rather than exact value.
func TestAdoptionIdentityPassThrough(t *testing.T) {
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

// A dev build's identity is never the raw sentinel: it is either "dev-<revision>" (clean)
// or the per-process devUnverifiable token (dirty / no VCS). Whichever this test binary is,
// the result must differ from "unknown" so two dev builds cannot match on the placeholder.
func TestAdoptionIdentityDevIsFingerprinted(t *testing.T) {
	got := adoptionIdentity(devVersionSentinel)
	assert.NotEqual(t, devVersionSentinel, got, "a dev build must not keep the shared placeholder as its identity")
	assert.NotEmpty(t, got, "a dev build's identity is never empty (that would disable the gate)")

	// Mirror adoptionIdentity's own decision: a build is unverifiable when it has no build
	// info, a dirty tree, or no embedded revision (go test binaries typically carry no
	// vcs.revision, so this branch is the common one under `go test`).
	rev, modified, ok := buildVCS()
	if !ok || modified || rev == "" {
		// Unprovable build: identity is the per-process token, which never matches anything.
		assert.Equal(t, devUnverifiable, got)
	} else {
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
