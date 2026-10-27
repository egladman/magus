package image

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

func readIndex(t *testing.T, layout string) ocispec.Index {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(layout, "index.json"))
	require.NoError(t, err)
	var idx ocispec.Index
	require.NoError(t, json.Unmarshal(data, &idx))
	return idx
}

func writeIndex(t *testing.T, layout string, idx ocispec.Index) {
	t.Helper()
	data, err := json.Marshal(idx)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(layout, "index.json"), data, 0o644))
}

// image-index.md: mediaType SHOULD be set on an index, index.json included.
func TestIndexJSONNamesItsMediaType(t *testing.T) {
	l := assembleOne(t, t.TempDir(), amd64)
	assert.Equal(t, ocispec.MediaTypeImageIndex, readIndex(t, l).MediaType)
}

// image-layout.md: index.json is a multi-descriptor entry point whose images are named
// by org.opencontainers.image.ref.name, and an unknown mediaType MUST NOT be an error.
func TestOpenLayoutSelectsAnImageByRefNameAndPassesOverUnknownEntries(t *testing.T) {
	root := t.TempDir()
	a := assembleOne(t, filepath.Join(root, "a"), amd64)
	b := assembleOne(t, filepath.Join(root, "b"), linuxARM64)
	idxA, idxB := readIndex(t, a), readIndex(t, b)
	require.NoError(t, os.Link(blobPath(b, idxB.Manifests[0].Digest), blobPath(a, idxB.Manifests[0].Digest)))

	first, second := idxA.Manifests[0], idxB.Manifests[0]
	first.Annotations = map[string]string{ocispec.AnnotationRefName: "v1-amd64"}
	second.Annotations = map[string]string{ocispec.AnnotationRefName: "v1-arm64"}
	unknown := ocispec.Descriptor{MediaType: "application/vnd.example.unknown+json", Digest: digest.FromString("elsewhere"), Size: 9}
	writeIndex(t, a, ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{unknown, first, second},
	})

	got, err := OpenLayout(t.Context(), a, "v1-arm64")
	require.NoError(t, err)
	assert.Equal(t, second.Digest, got.Descriptor.Digest, "selection is by ref.name, never by position")

	_, err = OpenLayout(t.Context(), a, "")
	require.ErrorContains(t, err, `holds 2 images (ref.name "v1-amd64", "v1-arm64"): name one`)
	_, err = OpenLayout(t.Context(), a, "v2")
	require.ErrorContains(t, err, `no image has ref.name "v2"`)
}

// A referrer is a manifest whose subject is the image, wherever index.json lists it; an
// entry after the image is not a referrer because of its position.
func TestOpenLayoutFindsReferrersBySubject(t *testing.T) {
	root := t.TempDir()
	l := assembleOne(t, root, amd64)
	sbom, err := WriteSBOM(t.Context(), l, filepath.Join(root, "sbom.json"))
	require.NoError(t, err)
	attached, err := AttachReferrers(t.Context(), l, []Document{sbom}, filepath.Join(root, "attached"))
	require.NoError(t, err)

	idx := readIndex(t, attached.Path)
	require.Len(t, idx.Manifests, 2)
	idx.Manifests[0], idx.Manifests[1] = idx.Manifests[1], idx.Manifests[0]
	idx.Manifests[0].ArtifactType = ""
	writeIndex(t, attached.Path, idx)

	opened, err := OpenLayout(t.Context(), attached.Path, "")
	require.NoError(t, err)
	assert.Equal(t, attached.Descriptor.Digest, opened.Descriptor.Digest)
	require.Len(t, opened.Referrers, 1)
	assert.Equal(t, attached.Referrers[0].Digest, opened.Referrers[0].Digest)
}

// image-index.md and config.md: platform carries os.version and os.features; they are
// written and read back, and take no part in matching.
func TestPlatformCarriesOSVersionAndOSFeatures(t *testing.T) {
	p := Platform{OS: "linux", Architecture: "amd64", Variant: "v3", OSVersion: "6.1", OSFeatures: []string{"example"}}
	m, err := Assemble(t.Context(), Spec{Scratch: true, Platform: p}, filepath.Join(t.TempDir(), "out"))
	require.NoError(t, err)
	cfg := readConfig(t, m)
	assert.Equal(t, "6.1", cfg.OSVersion)
	assert.Equal(t, []string{"example"}, cfg.OSFeatures)
	assert.Equal(t, "v3", cfg.Variant)

	opened, err := OpenLayout(t.Context(), m.Path, "")
	require.NoError(t, err)
	back, err := opened.Manifest(t.Context(), Platform{OS: "linux", Architecture: "amd64", Variant: "v3"})
	require.NoError(t, err)
	assert.Equal(t, p, back.Platform)
}

// layer.md: a .wh. basename is a whiteout, which deletes the base's path of that name;
// magus places no whiteouts, by any placement.
func TestAssembleRefusesAWhiteoutName(t *testing.T) {
	root := t.TempDir()
	src := write(t, filepath.Join(root, "f"), "x", 0o644)
	for _, p := range []string{"/etc/.wh.passwd", "/etc/.wh..wh..opq"} {
		err := Spec{Scratch: true, Platform: amd64, Files: []File{{Source: src, Path: p, Mode: ModeFile}}}.Validate()
		require.ErrorContains(t, err, "OCI whiteout", p)
	}
	tree := filepath.Join(root, "tree")
	write(t, filepath.Join(tree, "etc", ".wh.passwd"), "x", 0o644)
	_, err := Assemble(t.Context(), Spec{Scratch: true, Platform: amd64, Trees: []Tree{
		{Source: tree, Path: "/", ModePolicy: ModePolicyFixed, Symlinks: SymlinkPolicyRefuse},
	}}, filepath.Join(root, "out"))
	require.ErrorContains(t, err, "OCI whiteout")
}
