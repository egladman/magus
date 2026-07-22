package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildInfoFingerprint locks in the exact `magus --version` line assembled
// from the linker-stamped identity.
func TestBuildInfoFingerprint(t *testing.T) {
	b := BuildInfo{Version: "v1.2.3", Commit: "abc123", Date: "2026-01-01"}
	assert.Equal(t, "magus v1.2.3 (abc123) built 2026-01-01", b.Fingerprint())
}
