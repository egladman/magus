//go:build !wasm

package std

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

var linuxAMD64 = types.ImagePlatform{OS: types.ImageOSLinux, Architecture: types.ImageArchitectureAMD64}

func TestImageAssembleWritesNothingWhileTracing(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	_, err := ImageAssemble(types.WithTrace(t.Context()), types.ImageSpec{Scratch: true, Platform: linuxAMD64}, dest)
	require.NoError(t, err)
	assert.NoDirExists(t, dest, "a dry run traces the assemble and writes nothing")
}

func TestImageAssembleNamesFsModeForAZeroMode(t *testing.T) {
	src := filepath.Join(t.TempDir(), "f")
	require.NoError(t, os.WriteFile(src, []byte("x"), 0o644))
	_, err := ImageAssemble(t.Context(), types.ImageSpec{Scratch: true, Platform: linuxAMD64,
		Files: []types.ImageFile{{Source: src, Path: "/f"}}}, filepath.Join(t.TempDir(), "out"))
	require.ErrorContains(t, err, `fs\mode("0644")`)
}

func TestImageAssembleRefusesAnUnsetPlatform(t *testing.T) {
	_, err := ImageAssemble(t.Context(), types.ImageSpec{Scratch: true}, filepath.Join(t.TempDir(), "out"))
	require.ErrorContains(t, err, `platform os ""`, "an omitted platform is refused, never read as linux/amd64")
}

func TestImagePullRefusesATagOutsideTheUpdateCharm(t *testing.T) {
	_, err := ImagePull(t.Context(), "ghcr.io/acme/base:latest", linuxAMD64, filepath.Join(t.TempDir(), "base"), nil)
	require.ErrorContains(t, err, "update charm")
}

func TestImageAssembleThenOpenReadsTheLayoutBack(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "app")
	require.NoError(t, os.WriteFile(src, []byte("x"), 0o755))
	cmd := []string{"/app"}
	got, err := ImageAssemble(t.Context(), types.ImageSpec{Scratch: true, Platform: linuxAMD64,
		Files:  []types.ImageFile{{Source: src, Path: "/app", Mode: 0o755}},
		Config: types.ImageConfig{Entrypoint: &cmd}}, filepath.Join(root, "out"))
	require.NoError(t, err)
	assert.Equal(t, "sha256", got.Descriptor.Digest.Algorithm)
	assert.Equal(t, types.ImageMediaTypeManifest, got.Descriptor.MediaType)
	require.NotNil(t, got.Added)
	assert.Equal(t, 1, got.Added.Files)

	opened, err := ImageOpen(t.Context(), got.Path)
	require.NoError(t, err)
	assert.Equal(t, got.Descriptor.Digest, opened.Descriptor.Digest)
	require.NotNil(t, opened.Platform)
	assert.Equal(t, linuxAMD64, *opened.Platform)
}

func TestImageDigestString(t *testing.T) {
	assert.Equal(t, "sha256:abc", types.ImageDigest{Algorithm: "sha256", Encoded: "abc"}.String())
	assert.Empty(t, types.ImageDigest{}.String())
}

func TestFsModeParsesOctalAndRwx(t *testing.T) {
	got, err := FsMode(t.Context(), "rwxr-xr-x")
	require.NoError(t, err)
	assert.Equal(t, 0o755, got)
	_, err = FsMode(t.Context(), "0")
	require.Error(t, err)
}
