package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// floorWorkspace writes a minimal workspace whose magus.yaml declares constraint.
func floorWorkspace(t *testing.T, constraint string) string {
	t.Helper()
	root := t.TempDir()
	yaml := ""
	if constraint != "" {
		yaml = "required_version: \"" + constraint + "\"\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte(yaml), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"),
		[]byte("import \"magus\";\nexport fun ci(ctx: magus\\Context, args: [str]) > void {}\n"), 0o644))
	return root
}

// The wiring, not the comparison: internal/ward covers the semver logic, and this
// covers that magus.yaml's required_version actually reaches it. That path is easy to
// break invisibly - the field carries `cli:"-"`, so it is absent from the generated
// flag/env/schema tables, and a loader that read those instead of the struct tags
// would silently see an empty floor and admit every binary.
//
// It also cannot be exercised by a dev build: an unstamped binary reports "unknown"
// and the ward exempts it by design, so `go run ./cmd/magus` against a floored
// workspace always passes. A version has to be injected to test this at all.
func TestRequiredVersion_TooOldBinaryIsRefused(t *testing.T) {
	root := floorWorkspace(t, ">= 99.0.0")

	_, err := Inspect(context.Background(), root, WithVersion("v0.3.0"))

	require.Error(t, err, "a binary below the declared floor must not open the workspace")
	assert.ErrorIs(t, err, types.WorkspaceNeedsNewerMagus)
	assert.Contains(t, err.Error(), ">= 99.0.0")
	assert.Contains(t, err.Error(), "v0.3.0")
}

func TestRequiredVersion_SatisfiedBinaryOpens(t *testing.T) {
	root := floorWorkspace(t, ">= 0.3.0")

	_, err := Inspect(context.Background(), root, WithVersion("v0.4.0"))

	assert.NoError(t, err)
}

// No floor declared is the overwhelmingly common case and must stay free.
func TestRequiredVersion_NoFloorOpens(t *testing.T) {
	root := floorWorkspace(t, "")

	_, err := Inspect(context.Background(), root, WithVersion("v0.1.0"))

	assert.NoError(t, err)
}

// A library caller that never supplied a version has no version to be too old.
func TestRequiredVersion_NoVersionSuppliedOpens(t *testing.T) {
	root := floorWorkspace(t, ">= 99.0.0")

	_, err := Inspect(context.Background(), root)

	assert.NoError(t, err, "magus.Open/Inspect without WithVersion must not be gated")
}
