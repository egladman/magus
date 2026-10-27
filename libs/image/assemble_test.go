package image

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func imageTestReadBlob(t *testing.T, layout string, desc ocispec.Descriptor) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(layout, "blobs", "sha256", desc.Digest.Encoded()))
	require.NoError(t, err)
	assert.Equal(t, ocispec.Descriptor{
		MediaType: desc.MediaType, Digest: digest.FromBytes(data), Size: int64(len(data)), Platform: desc.Platform,
	}, desc)
	return data
}

func imageTestReadManifest(t *testing.T, layout string) (ocispec.Descriptor, ocispec.Manifest, ocispec.Image) {
	t.Helper()
	marker, err := os.ReadFile(filepath.Join(layout, "oci-layout"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"imageLayoutVersion":"1.0.0"}`, string(marker))
	indexData, err := os.ReadFile(filepath.Join(layout, "index.json"))
	require.NoError(t, err)
	var index ocispec.Index
	require.NoError(t, json.Unmarshal(indexData, &index))
	require.Len(t, index.Manifests, 1)
	root := index.Manifests[0]
	var manifest ocispec.Manifest
	require.NoError(t, json.Unmarshal(imageTestReadBlob(t, layout, root), &manifest))
	var cfg ocispec.Image
	require.NoError(t, json.Unmarshal(imageTestReadBlob(t, layout, manifest.Config), &cfg))
	return root, manifest, cfg
}

func imageTestLayerEntries(t *testing.T, data []byte) ([]tar.Header, digest.Digest) {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	tarData, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.NoError(t, zr.Close())
	tr := tar.NewReader(bytes.NewReader(tarData))
	var entries []tar.Header
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		entries = append(entries, *h)
		_, err = io.Copy(io.Discard, tr)
		require.NoError(t, err)
	}
	return entries, digest.FromBytes(tarData)
}

func TestImageAssembleIndependentReader(t *testing.T) {
	src := filepath.Join(t.TempDir(), "program")
	require.NoError(t, os.WriteFile(src, []byte("hello image"), 0o600))
	dest := filepath.Join(t.TempDir(), "layout")
	p := Platform{OS: "linux", Architecture: "amd64"}
	got, err := AssembleLayout(context.Background(), Spec{Platform: p,
		Files:    []File{{Source: src, Destination: "/usr/bin/program", Mode: 0o755, UID: 3, GID: 4}},
		Symlinks: []Symlink{{Destination: "/program", Target: "/usr/bin/program"}},
		Env:      map[string]string{"A": "1"}, Created: "2026-10-23T12:00:00Z",
	}, dest)
	require.NoError(t, err)
	root, manifest, cfg := imageTestReadManifest(t, dest)
	assert.Equal(t, Layout{
		Path: dest, Descriptor: root, Platform: p, Op: OpAssembleLayout,
		FileCount: 1, SymlinkCount: 1, Layer: got.Layer, ConfigDigest: got.ConfigDigest,
		Created: "2026-10-23T12:00:00Z",
	}, got)
	assert.False(t, got.Layer.IsZero())
	assert.Equal(t, "sha256", got.Layer.Digest.Algorithm())
	assert.Equal(t, ocispec.MediaTypeImageLayerGzip, got.Layer.MediaType)
	assert.Equal(t, ocispec.MediaTypeImageManifest, root.MediaType)
	assert.Equal(t, ocispec.MediaTypeImageConfig, manifest.Config.MediaType)
	require.Len(t, manifest.Layers, 1)
	layer := manifest.Layers[0]
	assert.Equal(t, ocispec.MediaTypeImageLayerGzip, layer.MediaType)
	entries, diffID := imageTestLayerEntries(t, imageTestReadBlob(t, dest, layer))
	assert.Equal(t, []digest.Digest{diffID}, cfg.RootFS.DiffIDs)
	assert.Equal(t, []tar.Header{
		{Typeflag: tar.TypeSymlink, Name: "program", Linkname: "/usr/bin/program", Mode: 0o777, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR},
		{Typeflag: tar.TypeDir, Name: "usr/", Mode: 0o755, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR},
		{Typeflag: tar.TypeDir, Name: "usr/bin/", Mode: 0o755, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR},
		{Typeflag: tar.TypeReg, Name: "usr/bin/program", Mode: 0o755, Uid: 3, Gid: 4, Size: 11, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR},
	}, entries)
	assert.Equal(t, []string{"A=1"}, cfg.Config.Env)
	assert.Equal(t, "2026-10-23T12:00:00Z", cfg.Created.Format(time.RFC3339))
}

func TestImageAssembleBaseInheritanceAndEmptyLayer(t *testing.T) {
	p := Platform{OS: "linux", Architecture: "amd64"}
	src := filepath.Join(t.TempDir(), "base-file")
	require.NoError(t, os.WriteFile(src, []byte("base"), 0o600))
	basePath := filepath.Join(t.TempDir(), "base")
	_, err := AssembleLayout(context.Background(), Spec{Platform: p, Files: []File{{Source: src, Destination: "/base", Mode: 0o644}}, Env: map[string]string{"A": "1"}, User: "42", Entrypoint: []string{"/base"}, Command: []string{"run"}, Created: "2026-01-02T03:04:05Z"}, basePath)
	require.NoError(t, err)
	baseRoot, baseManifest, baseCfg := imageTestReadManifest(t, basePath)
	dest := filepath.Join(t.TempDir(), "derived")
	derived, err := AssembleLayout(context.Background(), Spec{Base: basePath, Platform: p, Env: map[string]string{"B": "2"}}, dest)
	require.NoError(t, err)
	assert.Equal(t, "2026-01-02T03:04:05Z", derived.Created)
	_, manifest, cfg := imageTestReadManifest(t, dest)
	assert.Equal(t, baseManifest.Layers, manifest.Layers)
	wantCfg := baseCfg
	wantCfg.Config.Env = []string{"A=1", "B=2"}
	wantCfg.History = append(slices.Clone(baseCfg.History), ocispec.History{CreatedBy: "magus image write", EmptyLayer: true})
	assert.Equal(t, wantCfg, cfg)
	assert.Equal(t, imageTestReadBlob(t, basePath, baseRoot), imageTestReadBlob(t, dest, baseRoot))
	assert.Equal(t, imageTestReadBlob(t, basePath, baseManifest.Config), imageTestReadBlob(t, dest, baseManifest.Config))
	for _, layer := range baseManifest.Layers {
		assert.Equal(t, imageTestReadBlob(t, basePath, layer), imageTestReadBlob(t, dest, layer))
	}
	_, err = AssembleLayout(context.Background(), Spec{Base: basePath, Platform: Platform{OS: "linux", Architecture: "arm64"}}, filepath.Join(t.TempDir(), "bad"))
	require.ErrorContains(t, err, "base config platform")
}

func TestImageAssembleMissingBaseIsLocalOnly(t *testing.T) {
	t.Parallel()
	p := Platform{OS: "linux", Architecture: "amd64"}
	missing := filepath.Join(t.TempDir(), "no-such-layout")
	_, err := AssembleLayout(context.Background(), Spec{Base: missing, Platform: p}, filepath.Join(t.TempDir(), "out"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "local layout only")
	assert.ErrorContains(t, err, "update charm")
	assert.ErrorContains(t, err, missing)
	assert.False(t, errors.Is(err, context.Canceled), "assemble must not touch the network for a missing base")
}

func TestImageAssembleScratchWithoutLayer(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "layout")
	p := Platform{OS: "linux", Architecture: "amd64"}
	_, err := AssembleLayout(context.Background(), Spec{Platform: p}, dest)
	require.NoError(t, err)
	_, manifest, cfg := imageTestReadManifest(t, dest)
	assert.Equal(t, []ocispec.Descriptor{}, manifest.Layers)
	assert.Equal(t, ocispec.Image{
		Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"},
		RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{}},
		History:  []ocispec.History{{CreatedBy: "magus image write", EmptyLayer: true}},
	}, cfg)
}

func TestImageAssembleChecksEveryBaseLayerDescriptor(t *testing.T) {
	p := Platform{OS: "linux", Architecture: "amd64"}
	basePath := imageTestPlatformLayout(t, p)
	root, manifest, _ := imageTestReadManifest(t, basePath)
	duplicate := manifest.Layers[0]
	duplicate.Size++
	manifest.Layers = append(manifest.Layers, duplicate)
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	root.Digest, root.Size = digest.FromBytes(data), int64(len(data))
	require.NoError(t, os.WriteFile(filepath.Join(basePath, "blobs", "sha256", root.Digest.Encoded()), data, 0o644))
	indexPath := filepath.Join(basePath, "index.json")
	indexData, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	var index ocispec.Index
	require.NoError(t, json.Unmarshal(indexData, &index))
	index.Manifests[0] = root
	indexData, err = json.Marshal(index)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(indexPath, indexData, 0o644))

	dest := filepath.Join(t.TempDir(), "derived")
	_, err = AssembleLayout(context.Background(), Spec{Base: basePath, Platform: p}, dest)
	require.ErrorContains(t, err, "layer")
	require.ErrorContains(t, err, "size")
	_, err = os.Stat(dest)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestImageAssembleConfigOnlyKeepsLayerDigest(t *testing.T) {
	p := Platform{OS: "linux", Architecture: "amd64"}
	src := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(src, []byte("same"), 0o600))
	write := func(env string) (ocispec.Manifest, ocispec.Image) {
		t.Helper()
		dest := filepath.Join(t.TempDir(), "layout")
		_, err := AssembleLayout(context.Background(), Spec{Platform: p, Files: []File{{Source: src, Destination: "/file", Mode: 0o644}}, Env: map[string]string{"A": env}}, dest)
		require.NoError(t, err)
		_, m, c := imageTestReadManifest(t, dest)
		return m, c
	}
	m1, c1 := write("1")
	m2, c2 := write("2")
	assert.Equal(t, m1.Layers, m2.Layers)
	assert.Equal(t, c1.RootFS.DiffIDs, c2.RootFS.DiffIDs)
	assert.NotEqual(t, m1.Config.Digest, m2.Config.Digest)
}

func TestImageAssembleDeterministicAcrossHostState(t *testing.T) {
	p := Platform{OS: "linux", Architecture: "amd64"}
	build := func(reverse bool, stamp time.Time) string {
		t.Helper()
		root := t.TempDir()
		names := []string{"a", "b"}
		if reverse {
			slices.Reverse(names)
		}
		for _, name := range names {
			path := filepath.Join(root, name)
			require.NoError(t, os.WriteFile(path, []byte(name), 0o600))
			require.NoError(t, os.Chtimes(path, stamp, stamp))
		}
		dest := filepath.Join(t.TempDir(), "layout")
		_, err := AssembleLayout(context.Background(), Spec{Platform: p, Files: []File{
			{Source: filepath.Join(root, names[0]), Destination: "/" + names[0], Mode: 0o644},
			{Source: filepath.Join(root, names[1]), Destination: "/" + names[1], Mode: 0o644},
		}}, dest)
		require.NoError(t, err)
		return dest
	}
	oldProc := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProc)
	oldMask := syscall.Umask(0o077)
	defer syscall.Umask(oldMask)
	first := build(false, time.Unix(1, 0))
	syscall.Umask(0o022)
	runtime.GOMAXPROCS(4)
	second := build(true, time.Unix(999999, 0))
	compareImageTrees(t, first, second)
}

func compareImageTrees(t *testing.T, a, b string) {
	t.Helper()
	assert.Equal(t, snapshot(t, a), snapshot(t, b))
}
