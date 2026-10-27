package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/embedtable"
	"github.com/egladman/magus/libs/oci"
	"github.com/egladman/magus/libs/embed"
)

// tableCache points the XDG cache at a fresh directory and returns the path the
// table would occupy in it.
func tableCache(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	resetStartupSingletons()
	t.Cleanup(resetStartupSingletons)
	path, err := embedtable.Options{}.Path()
	require.NoError(t, err)
	return path
}

func TestGraphTablePullRefusesALayerOtherThanThePin(t *testing.T) {
	path := tableCache(t)
	reg := newSpellRegistry(t)
	pin, err := embed.Pin()
	require.NoError(t, err)
	ref := oci.Reference{Registry: reg.host(), Repository: "egladman/magus-embed", Tag: pin}
	wrong := oci.Content{ArtifactType: "application/vnd.magus.embed-table.v1", Layers: []oci.Layer{
		{Name: filepath.Base(path), MediaType: "application/vnd.magus.embed-table.layer.v1", Payload: []byte("other bytes")},
	}}
	_, err = (&oci.Client{HTTP: reg.srv.Client()}).Push(t.Context(), ref, wrong)
	require.NoError(t, err)

	err = graphPull(t.Context(), t.TempDir(), []string{"-table", "--ref", reg.host() + "/egladman/magus-embed"})
	require.ErrorContains(t, err, "the pin is sha256:"+pin)
	_, statErr := os.Stat(path)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "a refused layer leaves the cache as it was")
}

func TestGraphTablePullRefusesOut(t *testing.T) {
	tableCache(t)
	err := graphPull(t.Context(), t.TempDir(), []string{"-table", "--out", filepath.Join(t.TempDir(), "x")})
	require.ErrorContains(t, err, "--out names nothing")
}

func TestGraphTablePushRefusesATableOffThePin(t *testing.T) {
	path := tableCache(t)
	reg := newSpellRegistry(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("a table from another conversion"), 0o644))

	var err error
	withStdin(t, "token", func() {
		err = graphPush(t.Context(), t.TempDir(), []string{"-table", "--ref", reg.host() + "/egladman/magus-embed", "--username", "u"})
	})
	require.ErrorContains(t, err, "never published under another tag")
	assert.Zero(t, reg.uploads)
}

func TestGraphTablePushWithoutATableNamesTheCommand(t *testing.T) {
	tableCache(t)
	reg := newSpellRegistry(t)
	var err error
	withStdin(t, "token", func() {
		err = graphPush(t.Context(), t.TempDir(), []string{"-table", "--ref", reg.host() + "/egladman/magus-embed", "--username", "u"})
	})
	require.ErrorContains(t, err, "no embedding table in the cache")
	assert.Contains(t, err.Error(), "magus run table libs/embed/train")
	assert.Zero(t, reg.uploads)
}
