package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// TestCommandID pins the command node ID format: "command:<project>:<target>:<spell>",
// mirroring targetID/opID. The rendered argv is never part of the ID (it rides an attr),
// so identity stays stable across argv edits.
func TestCommandID(t *testing.T) {
	assert.Equal(t, "command:pkg/a:build:go", commandID("pkg/a", "build", "go"))
	assert.Equal(t, types.KindCommand+":.:lint:md", commandID(".", "lint", "md"))
}
