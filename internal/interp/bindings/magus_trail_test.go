package bindings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrailNamespaceShapesACommand(t *testing.T) {
	call := execMagusScript(t, &checkoutWorkspace{jobNamespaceWorkspace{root: "."}}, `
import "magus";

export fun shape() > str {
    return magus\trail.shape("grep -rn foo src");
}

export fun shapes() > int {
    return magus\trail.shapes("cd x && grep -rn foo src | head -5").len();
}
`)
	got, err := call("shape")
	require.NoError(t, err)
	assert.Equal(t, "grep -rn <arg>", got.AsString())

	n, err := call("shapes")
	require.NoError(t, err)
	assert.Equal(t, int64(3), n.AsInt())
}
