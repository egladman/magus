package bindings

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boundedTool(b spells.VersionBounds) map[string]spells.Tool {
	return map[string]spells.Tool{
		"faketool": {
			Probe:     spells.Command{Bin: "sh", Args: []string{"-c", "echo 'faketool 1.4.2'"}},
			Supported: b,
		},
	}
}

// A too-old tool must be named as such before the op forks. Without this it fails with
// whatever the tool says about an unrecognized flag - the misleading failure readiness
// exists to prevent, one question over.
func TestBoundsRejectsTooOld(t *testing.T) {
	readinessMemo.Clear()
	err := checkBounds(context.Background(), boundedTool(spells.VersionBounds{Min: "2.0"}), t.TempDir())
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.ToolTooOld), "want MGS3005, got %v", err)
	assert.Contains(t, err.Error(), "1.4.2")
	assert.Contains(t, err.Error(), "2.0")
}

// The defect the two-bound shape exists to make unrepresentable: under the single
// constraint string this reported MGS3005 and told the reader a 1.4.2 binary was
// "older than this spell supports" when the problem was that it was too new.
func TestBoundsRejectsTooNewAsItsOwnCode(t *testing.T) {
	readinessMemo.Clear()
	err := checkBounds(context.Background(), boundedTool(spells.VersionBounds{Below: "1.4"}), t.TempDir())
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.ToolTooNew), "want MGS3006, got %v", err)
	assert.False(t, errors.Is(err, types.ToolTooOld), "a too-new tool must not report MGS3005")
	assert.Contains(t, err.Error(), "1.4.2")
}

// The ceiling is exclusive, so the version it names is the first one rejected and
// everything under it passes. This is the off-by-one an inclusive max invites.
func TestBoundsCeilingIsExclusive(t *testing.T) {
	readinessMemo.Clear()
	assert.NoError(t, checkBounds(context.Background(), boundedTool(spells.VersionBounds{Below: "2"}), t.TempDir()))

	readinessMemo.Clear()
	err := checkBounds(context.Background(), boundedTool(spells.VersionBounds{Below: "1.4.2"}), t.TempDir())
	assert.Error(t, err, "below is the first version REJECTED, so an exact match fails")
}

func TestBoundsAcceptsInsideWindow(t *testing.T) {
	readinessMemo.Clear()
	assert.NoError(t, checkBounds(context.Background(),
		boundedTool(spells.VersionBounds{Min: "1.0", Below: "2.0"}), t.TempDir()))
}

// A tool declaring no window is never probed for one.
func TestBoundsAbsentIsUngated(t *testing.T) {
	readinessMemo.Clear()
	assert.NoError(t, checkBounds(context.Background(), boundedTool(spells.VersionBounds{}), t.TempDir()))
}

// Unprobeable is not "outside the window". Failing here would make a bound a second way
// for a missing tool to break, with a worse message than MGS3003's.
func TestBoundsIgnoresUnprobeableTool(t *testing.T) {
	readinessMemo.Clear()
	tools := map[string]spells.Tool{
		"faketool": {
			Probe:     spells.Command{Bin: "definitely-not-a-real-binary-xyz"},
			Supported: spells.VersionBounds{Min: "99.0"},
		},
	}
	assert.NoError(t, checkBounds(context.Background(), tools, t.TempDir()))
}
