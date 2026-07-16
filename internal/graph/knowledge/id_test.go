package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// TestToolID pins the tool node ID format: "tool:<program>", the workspace-scoped node
// an op (and its spell) uses for the program it runs.
func TestToolID(t *testing.T) {
	assert.Equal(t, "tool:go", toolID("go"))
	assert.Equal(t, types.KindTool+":sh", toolID("sh"))
}
