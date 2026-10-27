package image

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSpecWithChainAssemblesLayout(t *testing.T) {
	src := filepath.Join(t.TempDir(), "bin")
	require.NoError(t, os.WriteFile(src, []byte("hello"), 0o600))
	dest := filepath.Join(t.TempDir(), "layout")

	mode, err := NewMode(0o755)
	require.NoError(t, err)
	file := HostFile{Value: src}.Place(ImageFile{Value: "/usr/bin/app"}, mode)

	layout, err := NewSpec(NewLinuxAmd64()).
		WithFile(file).
		WithEnv("APP", "1").
		WithEntrypoint("/usr/bin/app").
		WithUser("65532").
		AssembleLayout(context.Background(), dest)
	require.NoError(t, err)
	assert.Equal(t, OpAssembleLayout, layout.Op)
	assert.False(t, layout.Layer.IsZero())
	assert.Equal(t, dest, layout.Path)

	_, manifest, cfg := imageTestReadManifest(t, dest)
	require.Len(t, manifest.Layers, 1)
	assert.Equal(t, []string{"/usr/bin/app"}, cfg.Config.Entrypoint)
	assert.Equal(t, "65532", cfg.Config.User)
	assert.Contains(t, cfg.Config.Env, "APP=1")
}

func TestNilSpecAssembleLayoutErrors(t *testing.T) {
	var s *Spec
	_, err := s.AssembleLayout(context.Background(), t.TempDir())
	assert.ErrorContains(t, err, "nil spec")
}
