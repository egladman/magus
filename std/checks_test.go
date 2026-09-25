//go:build !wasm

package std

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckReadWriteResolveAgainstTheCwd: a relative path is judged where it lands, the
// project dir on ctx, not the process cwd.
func TestCheckReadWriteResolveAgainstTheCwd(t *testing.T) {
	ws := filesystem.ResolveRulePath(t.TempDir())
	ctx := WithCwd(sandbox.WithPolicy(context.Background(), &sandbox.Policy{
		FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: ws, Read: true}}},
	}), ws)

	require.NoError(t, CheckRead(ctx, "inside.txt"))
	require.ErrorIs(t, CheckWrite(ctx, "inside.txt"), types.PathWriteDenied)
	require.ErrorIs(t, CheckRead(ctx, filepath.Join("..", "outside.txt")), types.PathReadDenied)

	assert.NoError(t, CheckWrite(context.Background(), "/anywhere"), "no policy, no check")
}
