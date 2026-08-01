package std

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSemverNext(t *testing.T) {
	got, err := SemverNext(context.Background(), "v0.1.0")
	require.NoError(t, err)
	assert.Equal(t, types.SemverNext{
		Major: "v1.0.0",
		Minor: "v0.2.0",
		Patch: "v0.1.1",
	}, got)
}

// TestSemverNextPatchStripsPrerelease pins the spec subtlety the doc comment on
// SemverNext calls out: Masterminds/semver's IncPatch on a version WITH a
// prerelease strips the prerelease and KEEPS the patch number rather than
// bumping it (semver.org spec item 9). Hand-rolled "+1" arithmetic gets this
// wrong, which is the whole reason SemverNext delegates instead of computing it.
func TestSemverNextPatchStripsPrerelease(t *testing.T) {
	got, err := SemverNext(context.Background(), "v1.2.3-rc1")
	require.NoError(t, err)
	assert.Equal(t, "v1.2.3", got.Patch, "IncPatch on a prerelease strips it without bumping the patch number")
}

func TestSemverNextInvalidInput(t *testing.T) {
	_, err := SemverNext(context.Background(), "not-a-version")
	assert.Error(t, err)
}
