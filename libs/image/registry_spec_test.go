package image

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/oci"
)

var (
	linuxARM64 = Platform{OS: "linux", Architecture: "arm64"}
)

func (r *registry) armIndex() (idx, arm ocispec.Descriptor) {
	amd := r.publish(amd64, "amd")
	arm = r.publish(Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}, "arm")
	amd.Platform = &ocispec.Platform{OS: "linux", Architecture: "amd64"}
	arm.Platform = &ocispec.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}
	idx = r.putJSON(ocispec.MediaTypeImageIndex, ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{amd, arm},
	})
	return idx, arm
}

func TestPullMatchesAnArm64IndexEntryListedAsV8(t *testing.T) {
	reg := newRegistry(t)
	idx, arm := reg.armIndex()

	got, err := Pull(t.Context(), reg.client(), pinned(reg.repository(), idx.Digest), linuxARM64, filepath.Join(t.TempDir(), "base"))
	require.NoError(t, err)
	assert.Equal(t, arm.Digest, got.Descriptor.Digest)
	assert.Equal(t, "v8", got.Platform.Variant)
}

// annotations.md: base.digest is the digest of the manifest base.name references. A pin
// on an index must not leave base.name naming the index while base.digest names the
// platform manifest.
func TestBaseNameAndBaseDigestNameTheSameManifest(t *testing.T) {
	reg := newRegistry(t)
	idx, arm := reg.armIndex()
	root := t.TempDir()
	base := filepath.Join(root, "base")
	_, err := Pull(t.Context(), reg.client(), pinned(reg.repository(), idx.Digest), linuxARM64, base)
	require.NoError(t, err)

	m := assembleOver(t, root, base, linuxARM64)
	annotations := readManifest(t, m).Annotations
	assert.Equal(t, arm.Digest.String(), annotations[ocispec.AnnotationBaseImageDigest])
	name, err := oci.ParseReference(annotations[ocispec.AnnotationBaseImageName])
	require.NoError(t, err)
	assert.Equal(t, arm.Digest, name.Digest)
	assert.Equal(t, reg.repository().Repository, name.Repository)
}

func assembleOver(t *testing.T, root, base string, p Platform) Manifest {
	t.Helper()
	bin := write(t, filepath.Join(root, "app"), "app", 0o755)
	m, err := Assemble(t.Context(), Spec{
		Base: base, Platform: p,
		Files: []File{{Source: bin, Path: "/app", Mode: ModeExecutable}},
	}, filepath.Join(root, "over"))
	require.NoError(t, err)
	return m
}

func (r *registry) publishDocker(p Platform) (list, manifest, layer ocispec.Descriptor) {
	layer = r.put(mediaTypeDockerLayerGzip, tarArchive(r.t, map[string]string{"etc/os-release": "docker"}))
	cfg := r.put(mediaTypeDockerConfig, []byte(`{"architecture":"`+p.Architecture+`","os":"linux","config":{"Cmd":["/bin/sh"],"Healthcheck":{"Test":["CMD","true"]}},"rootfs":{"type":"layers","diff_ids":[]},"docker_version":"24.0.0"}`))
	manifest = r.putJSON(mediaTypeDockerManifest, map[string]any{
		"schemaVersion": 2, "mediaType": mediaTypeDockerManifest, "config": cfg, "layers": []ocispec.Descriptor{layer},
	})
	manifest.Platform = p.oci()
	list = r.putJSON(mediaTypeDockerManifestList, map[string]any{
		"schemaVersion": 2, "mediaType": mediaTypeDockerManifestList, "manifests": []ocispec.Descriptor{manifest},
	})
	return list, manifest, layer
}

// media-types.md: Docker's schema 2 types are interchangeable with OCI's, and a copier
// MUST NOT error on them. Assembling over one rewrites it as OCI.
func TestPullCopiesADockerImageAndAssembleConvertsIt(t *testing.T) {
	reg := newRegistry(t)
	list, manifest, layer := reg.publishDocker(amd64)
	root := t.TempDir()
	base := filepath.Join(root, "base")

	pulled, err := Pull(t.Context(), reg.client(), pinned(reg.repository(), list.Digest), amd64, base)
	require.NoError(t, err)
	assert.Equal(t, manifest.Digest, pulled.Descriptor.Digest, "the manifest bytes are kept as served")
	assert.Equal(t, mediaTypeDockerManifest, pulled.Descriptor.MediaType)
	assert.Equal(t, []ocispec.Descriptor{layer}, pulled.Layers)

	m := assembleOver(t, root, base, amd64)
	man := readManifest(t, m)
	assert.Equal(t, ocispec.MediaTypeImageManifest, man.MediaType)
	assert.Equal(t, ocispec.MediaTypeImageConfig, man.Config.MediaType)
	require.Len(t, man.Layers, 2)
	assert.Equal(t, ocispec.MediaTypeImageLayerGzip, man.Layers[0].MediaType)
	assert.Equal(t, layer.Digest, man.Layers[0].Digest, "the base layer's bytes are kept")
	assert.Equal(t, manifest.Digest.String(), man.Annotations[ocispec.AnnotationBaseImageDigest])
}

// config.md: extra fields MUST NOT be an error; a rebuild that dropped them would lose a
// base's Healthcheck without a word.
func TestAssembleKeepsTheBaseConfigFieldsItDoesNotModel(t *testing.T) {
	reg := newRegistry(t)
	list, _, _ := reg.publishDocker(amd64)
	root := t.TempDir()
	base := filepath.Join(root, "base")
	_, err := Pull(t.Context(), reg.client(), pinned(reg.repository(), list.Digest), amd64, base)
	require.NoError(t, err)

	m := assembleOver(t, root, base, amd64)
	data, err := os.ReadFile(blobPath(m.Path, m.Config.Digest))
	require.NoError(t, err)
	var cfg struct {
		DockerVersion string `json:"docker_version"`
		Config        struct {
			Cmd         []string        `json:"Cmd"`
			Healthcheck json.RawMessage `json:"Healthcheck"`
		} `json:"config"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))
	assert.Equal(t, "24.0.0", cfg.DockerVersion)
	assert.JSONEq(t, `{"Test":["CMD","true"]}`, string(cfg.Config.Healthcheck))
	assert.Equal(t, []string{"/bin/sh"}, cfg.Config.Cmd)

	cleared, err := Assemble(t.Context(), Spec{Base: base, Platform: amd64, Config: Config{Cmd: &[]string{}}}, filepath.Join(root, "cleared"))
	require.NoError(t, err)
	assert.Empty(t, readConfig(t, cleared).Config.Cmd, "a modelled field cleared on purpose is not copied back from the base")
}

// manifest.md: implementations MUST support the non-distributable layer types; a
// registry need not hold the blob, so Pull describes it and Assemble refuses to build on
// a layer it cannot copy.
func TestPullDescribesANonDistributableLayerAndAssembleRefusesIt(t *testing.T) {
	reg := newRegistry(t)
	foreign := ocispec.Descriptor{
		MediaType: "application/vnd.oci.image.layer.nondistributable.v1.tar+gzip",
		Digest:    digest.FromString("held elsewhere"), Size: 14,
		URLs: []string{"https://example.com/layer.tar.gz"},
	}
	cfg := reg.putJSON(ocispec.MediaTypeImageConfig, ocispec.Image{
		Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"},
		RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{foreign.Digest}},
	})
	man := reg.putJSON(ocispec.MediaTypeImageManifest, ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest,
		Config: cfg, Layers: []ocispec.Descriptor{foreign},
	})
	root := t.TempDir()
	base := filepath.Join(root, "base")

	pulled, err := Pull(t.Context(), reg.client(), pinned(reg.repository(), man.Digest), amd64, base)
	require.NoError(t, err)
	assert.Equal(t, []ocispec.Descriptor{foreign}, pulled.Layers)

	_, err = Assemble(t.Context(), Spec{Base: base, Platform: amd64}, filepath.Join(root, "over"))
	require.ErrorContains(t, err, "is non-distributable")
}

func TestPullRefusesATagAndLeavesDestAlone(t *testing.T) {
	reg := newRegistry(t)
	m := reg.publish(amd64, "x")
	reg.put(m.MediaType, mustManifest(t, reg, m), "v1")
	ref := reg.repository()
	ref.Tag = "v1"
	dest := filepath.Join(t.TempDir(), "base")
	require.NoError(t, os.MkdirAll(dest, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "keep"), []byte("x"), 0o644))

	_, err := Pull(t.Context(), reg.client(), ref, amd64, dest)
	require.ErrorContains(t, err, "names a tag")
	assert.FileExists(t, filepath.Join(dest, "keep"), "a refused pull leaves dest as it was")

	_, err = Pull(t.Context(), nil, pinned(reg.repository(), m.Digest), amd64, dest)
	require.ErrorContains(t, err, "no oci.Client")
}

func mustManifest(t *testing.T, reg *registry, d ocispec.Descriptor) []byte {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	m, ok := reg.manifests[d.Digest.String()]
	require.True(t, ok)
	return m.body
}

func TestPushThenPullRoundTripsAnAttachedIndex(t *testing.T) {
	reg := newRegistry(t)
	root := t.TempDir()
	idx := assembleIndex(t, root)
	sbom, err := WriteSBOM(t.Context(), idx, filepath.Join(root, "sbom.spdx.json"))
	require.NoError(t, err)
	attached, err := AttachReferrers(t.Context(), idx, []Document{sbom}, filepath.Join(root, "attached"))
	require.NoError(t, err)

	t.Setenv("MAGUS_OFFLINE", "0")
	pushed, err := Push(t.Context(), reg.client(), attached.Path, reg.repository(), "v1")
	require.NoError(t, err, "MAGUS_OFFLINE=0 means online, as libs/oci reads it")
	assert.Equal(t, pinned(reg.repository(), attached.Descriptor.Digest), pushed)

	for _, p := range []Platform{amd64, linuxARM64} {
		_, err := Pull(t.Context(), reg.client(), pushed, p, filepath.Join(root, "pulled-"+p.Architecture))
		require.NoError(t, err, "every platform manifest the index names reached the registry")
	}
	reg.mu.Lock()
	_, ok := reg.manifests[attached.Referrers[0].Digest.String()]
	tagged, hasTag := reg.manifests[oci.ReferrersTag(attached.Descriptor.Digest)]
	reg.mu.Unlock()
	assert.True(t, ok, "the referrer manifest is pushed")

	// This registry answers no OCI-Subject, so distribution-spec requires the fallback tag.
	require.True(t, hasTag, "the referrer is listed under the sha256-<hex> fallback tag")
	var listed ocispec.Index
	require.NoError(t, json.Unmarshal(tagged.body, &listed))
	require.Len(t, listed.Manifests, 1)
	assert.Equal(t, attached.Referrers[0].Digest, listed.Manifests[0].Digest)
	assert.Equal(t, MediaTypeSPDX, listed.Manifests[0].ArtifactType)
}

func TestPushSkipsBlobsTheRegistryHolds(t *testing.T) {
	reg := newRegistry(t)
	root := t.TempDir()
	l := assembleOne(t, root, amd64)
	_, err := Push(t.Context(), reg.client(), l, reg.repository())
	require.NoError(t, err)
	first := reg.uploadCount()
	assert.Positive(t, first)

	_, err = Push(t.Context(), reg.client(), l, reg.repository())
	require.NoError(t, err)
	assert.Equal(t, first, reg.uploadCount(), "a second push uploads nothing")
}

func TestPushRefusesAnOfflineClient(t *testing.T) {
	reg := newRegistry(t)
	l := assembleOne(t, t.TempDir(), amd64)
	t.Setenv("MAGUS_OFFLINE", "1")
	_, err := Push(context.Background(), reg.client(), l, reg.repository())
	require.True(t, errors.Is(err, oci.ErrOffline), "%v", err)
}
