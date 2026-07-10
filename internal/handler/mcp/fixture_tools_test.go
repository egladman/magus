package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/doctor"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureMagus opens a real single-project workspace (a go.mod plus one JS
// project marker) so tools that need a live *magus.Magus can be exercised.
func fixtureMagus(t *testing.T) *magus.Magus {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module mcptest\n"), 0o644))
	pkg := filepath.Join(root, "pkg")
	require.NoError(t, os.MkdirAll(pkg, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"pkg"}`), 0o644))
	m, err := magus.Open(context.Background(), root)
	require.NoError(t, err)
	return m
}

func TestDoctorToolAgainstFixture(t *testing.T) {
	tool := &doctorTool{opts: Options{Magus: fixtureMagus(t)}}
	assert.Equal(t, "magus_doctor", tool.Name())

	resp, err := tool.Invoke(context.Background(), types.InvokeRequest{})
	require.NoError(t, err)
	rep, ok := resp.Data.(doctor.Report)
	require.True(t, ok, "doctor tool should return a doctor.Report")
	assert.NotEmpty(t, rep.Checks, "doctor produced no checks")
}

func TestStatusToolNoDaemon(t *testing.T) {
	// Point discovery at a socket that cannot exist, so QueryStatus fails
	// deterministically rather than connecting to a real daemon on the dev box.
	t.Setenv("MAGUS_DAEMON_SOCKET", filepath.Join(t.TempDir(), "nonexistent.sock"))

	tool := &statusTool{opts: Options{Magus: fixtureMagus(t)}}
	assert.Equal(t, "magus_status", tool.Name())

	// status never returns an error; an unreachable daemon becomes a PoolError.
	resp, err := tool.Invoke(context.Background(), types.InvokeRequest{})
	require.NoError(t, err)
	got := resp.Data.(statusResult)
	assert.Nil(t, got.Pool, "no daemon should mean no pool reply")
	assert.NotEmpty(t, got.PoolError, "unreachable daemon should surface a pool_error")
}
