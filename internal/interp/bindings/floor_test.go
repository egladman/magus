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

func floorTool(floor string) map[string]spells.Tool {
	return map[string]spells.Tool{
		"faketool": {
			Probe: spells.Command{Bin: "sh", Args: []string{"-c", "echo 'faketool 1.4.2'"}},
			Floor: floor,
		},
	}
}

// A too-old tool must be named as such before the op forks. Without this it fails with
// whatever the tool says about an unrecognized flag - the misleading failure readiness
// exists to prevent, one question over.
func TestFloorRejectsTooOld(t *testing.T) {
	readinessMemo.Clear()
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}
	err := checkFloor(context.Background(), floorTool(">= 2.0"), op, t.TempDir())
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.ToolTooOld), "want MGS3005, got %v", err)
	assert.Contains(t, err.Error(), "1.4.2")
	assert.Contains(t, err.Error(), ">= 2.0")
}

func TestFloorAcceptsNewEnough(t *testing.T) {
	readinessMemo.Clear()
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}
	assert.NoError(t, checkFloor(context.Background(), floorTool(">= 1.0"), op, t.TempDir()))
}

// A tool declaring no floor is never probed for one.
func TestFloorAbsentIsUngated(t *testing.T) {
	readinessMemo.Clear()
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}
	assert.NoError(t, checkFloor(context.Background(), floorTool(""), op, t.TempDir()))
}

// Unprobeable is not "too old". Failing here would make a floor a second way for a
// missing tool to break, with a worse message than MGS3003's.
func TestFloorIgnoresUnprobeableTool(t *testing.T) {
	readinessMemo.Clear()
	tools := map[string]spells.Tool{
		"faketool": {Probe: spells.Command{Bin: "definitely-not-a-real-binary-xyz"}, Floor: ">= 99.0"},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}
	assert.NoError(t, checkFloor(context.Background(), tools, op, t.TempDir()))
}
