package image

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/oci"
)

var amd64 = Platform{OS: "linux", Architecture: "amd64"}

func write(t *testing.T, path, content string, mode os.FileMode) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), mode))
	require.NoError(t, os.Chmod(path, mode))
	return path
}

// layerHeaders reads the new layer of the image at layout back as tar headers.
func layerHeaders(t *testing.T, m ManifestLayout) map[string]*tar.Header {
	t.Helper()
	require.NotNil(t, m.Added)
	f, err := os.Open(blobPath(m.Path, m.Added.Layer.Digest))
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	out := map[string]*tar.Header{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		out[h.Name] = h
	}
}

func readManifest(t *testing.T, m ManifestLayout) ocispec.Manifest {
	t.Helper()
	data, err := os.ReadFile(blobPath(m.Path, m.Descriptor.Digest))
	require.NoError(t, err)
	var man ocispec.Manifest
	require.NoError(t, json.Unmarshal(data, &man))
	return man
}

func readConfig(t *testing.T, m ManifestLayout) ocispec.Image {
	t.Helper()
	data, err := os.ReadFile(blobPath(m.Path, m.Config.Digest))
	require.NoError(t, err)
	var cfg ocispec.Image
	require.NoError(t, json.Unmarshal(data, &cfg))
	return cfg
}

func TestAssembleIsDeterministic(t *testing.T) {
	root := t.TempDir()
	bin := write(t, filepath.Join(root, "app"), "binary", 0o755)
	spec := Spec{Scratch: true, Platform: amd64, Files: []File{{Source: bin, Path: "/usr/bin/app", Mode: ModeExecutable}}}
	a, err := Assemble(t.Context(), spec, filepath.Join(root, "a"))
	require.NoError(t, err)
	b, err := Assemble(t.Context(), spec, filepath.Join(root, "b"))
	require.NoError(t, err)
	assert.Equal(t, a.Descriptor.Digest, b.Descriptor.Digest)
	want := Added{Layer: b.Added.Layer, DiffID: b.Added.DiffID, Files: 1, Directories: 2}
	require.NotNil(t, a.Added)
	assert.Equal(t, want, *a.Added, "scratch gets /usr and /usr/bin")
}

func TestAssembleRefusesAPlacementBelowAFileAcrossASortedSibling(t *testing.T) {
	root := t.TempDir()
	src := write(t, filepath.Join(root, "f"), "x", 0o644)
	_, err := Assemble(t.Context(), Spec{Scratch: true, Platform: amd64, Files: []File{
		{Source: src, Path: "/app", Mode: ModeFile},
		{Source: src, Path: "/app-b", Mode: ModeFile},
		{Source: src, Path: "/app/x", Mode: ModeFile},
	}}, filepath.Join(root, "out"))
	require.ErrorContains(t, err, "/app/x is below /app, which is not a directory")
}

func TestTreePlacesContentsAndIncludeReachesNestedFiles(t *testing.T) {
	root := t.TempDir()
	tree := filepath.Join(root, "rootfs")
	write(t, filepath.Join(tree, "usr", "lib", "x86", "libz.so"), "z", 0o644)
	write(t, filepath.Join(tree, "usr", "lib", "x86", "README"), "r", 0o644)
	write(t, filepath.Join(tree, "bin", "tool"), "t", 0o755)
	m, err := Assemble(t.Context(), Spec{Scratch: true, Platform: amd64, Trees: []Tree{
		{Source: tree, Path: "/opt", Include: []string{"**/*.so", "bin/*"}, Modes: ModeExecutableBit, Symlinks: SymlinkRefuse},
	}}, filepath.Join(root, "out"))
	require.NoError(t, err)
	h := layerHeaders(t, m)
	require.Contains(t, h, "opt/usr/lib/x86/libz.so", "Include filters files and never prunes the directories above them")
	assert.NotContains(t, h, "opt/usr/lib/x86/README")
	assert.Equal(t, int64(0o755), h["opt/bin/tool"].Mode, "the host exec bit picks ModeExecutable")
	assert.Equal(t, int64(0o644), h["opt/usr/lib/x86/libz.so"].Mode)
}

func TestTreeRefusesABadPatternAndAnUnsetPolicy(t *testing.T) {
	spec := Spec{Scratch: true, Platform: amd64, Trees: []Tree{{Source: "x", Path: "/x", Include: []string{"[a-"}, Modes: ModeFixed, Symlinks: SymlinkRefuse}}}
	require.ErrorContains(t, spec.Validate(), "not a valid doublestar glob")
	spec.Trees[0].Include = nil
	spec.Trees[0].Modes = ""
	require.ErrorContains(t, spec.Validate(), "modes is unset")
}

func TestTreeKeepRefusesAnAbsoluteHostTarget(t *testing.T) {
	root := t.TempDir()
	tree := filepath.Join(root, "t")
	require.NoError(t, os.MkdirAll(tree, 0o755))
	require.NoError(t, os.Symlink("/etc/passwd", filepath.Join(tree, "link")))
	_, err := Assemble(t.Context(), Spec{Scratch: true, Platform: amd64, Trees: []Tree{
		{Source: tree, Path: "/t", Modes: ModeFixed, Symlinks: SymlinkKeep},
	}}, filepath.Join(root, "out"))
	require.ErrorContains(t, err, "names a host path")
}

func TestAssembleOverABaseReportsTheNewConfigAndAnnotatesTheBase(t *testing.T) {
	root := t.TempDir()
	base := assembleOne(t, root, amd64)
	extra := write(t, filepath.Join(root, "extra"), "e", 0o644)
	m, err := Assemble(t.Context(), Spec{Base: base, Platform: amd64,
		Files:  []File{{Source: extra, Path: "/etc/extra", Mode: ModeFile}},
		Config: Config{Cmd: &[]string{"serve"}},
	}, filepath.Join(root, "over"))
	require.NoError(t, err)

	man := readManifest(t, m)
	assert.Equal(t, man.Config, m.Config, "Config is the manifest's own config, never the base's")
	baseLayout, err := OpenLayout(t.Context(), base)
	require.NoError(t, err)
	assert.Equal(t, baseLayout.Descriptor.Digest.String(), man.Annotations[ocispec.AnnotationBaseImageDigest])
	baseManifest, err := baseLayout.Manifest(t.Context())
	require.NoError(t, err)
	want := readConfig(t, baseManifest).Config
	require.Equal(t, []string{"/app"}, want.Entrypoint)
	want.Cmd = []string{"serve"}
	assert.Equal(t, want, readConfig(t, m).Config, "a nil Entrypoint inherits; Cmd replaces the base's")
	assert.Len(t, m.Layers, 2)
}

func TestAssembleOverABaseKeepsTheBasesDirectories(t *testing.T) {
	root := t.TempDir()
	marker := write(t, filepath.Join(root, "m"), "m", 0o644)
	base, err := Assemble(t.Context(), Spec{Scratch: true, Platform: amd64,
		Directories: []Directory{{Path: "/tmp", Mode: ModeSticky}},
		Files:       []File{{Source: marker, Path: "/home/nonroot/.keep", Mode: ModeFile, UID: 65532, GID: 65532}},
	}, filepath.Join(root, "base"))
	require.NoError(t, err)
	assert.Equal(t, int64(0o1777), layerHeaders(t, base)["tmp/"].Mode)

	over, err := Assemble(t.Context(), Spec{Base: base.Path, Platform: amd64,
		Files: []File{{Source: marker, Path: "/tmp/x", Mode: ModeFile}, {Source: marker, Path: "/home/nonroot/y", Mode: ModeFile}},
	}, filepath.Join(root, "over"))
	require.NoError(t, err)
	h := layerHeaders(t, over)
	assert.NotContains(t, h, "tmp/", "an upper /tmp entry would reset the base's 1777")
	assert.NotContains(t, h, "home/nonroot/", "an upper entry would chown the base's home to root")
}

func TestAssembleNeverChangesALinkedBaseBlob(t *testing.T) {
	root := t.TempDir()
	base := assembleOne(t, root, amd64)
	opened, err := OpenLayout(t.Context(), base)
	require.NoError(t, err)
	m, err := opened.Manifest(t.Context())
	require.NoError(t, err)
	layer := blobPath(base, m.Layers[0].Digest)
	require.NoError(t, os.Chmod(layer, 0o600))

	_, err = Assemble(t.Context(), Spec{Base: base, Platform: amd64}, filepath.Join(root, "over"))
	require.NoError(t, err)
	info, err := os.Stat(layer)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the base layout's blob keeps its mode")
}

func TestAssembleRefusesModeZero(t *testing.T) {
	err := Spec{Scratch: true, Platform: amd64, Files: []File{{Source: "x", Path: "/x"}}}.Validate()
	require.ErrorContains(t, err, "mode is 0")
}

func TestAssembleRefusesAZeroSpec(t *testing.T) {
	require.ErrorContains(t, Spec{}.Validate(), "set base to a layout or scratch")
	require.ErrorContains(t, Spec{Scratch: true}.Validate(), `platform os ""`)
}

func TestAssembleWritesAShortUTF8Name(t *testing.T) {
	root := t.TempDir()
	src := write(t, filepath.Join(root, "f"), "x", 0o644)
	m, err := Assemble(t.Context(), Spec{Scratch: true, Platform: amd64,
		Files: []File{{Source: src, Path: "/données", Mode: ModeFile}}}, filepath.Join(root, "out"))
	require.NoError(t, err)
	assert.Contains(t, layerHeaders(t, m), "données")
}

func TestAssembleKeepsTheCreatedOffset(t *testing.T) {
	root := t.TempDir()
	m, err := Assemble(t.Context(), Spec{Scratch: true, Platform: amd64, Created: "2026-01-02T03:04:05+02:00"}, filepath.Join(root, "out"))
	require.NoError(t, err)
	assert.Equal(t, "2026-01-02T03:04:05+02:00", m.Created, "the receipt says what the config bytes say")
}

func TestConfigEmptyClearsAndExposedPortsLand(t *testing.T) {
	root := t.TempDir()
	base := assembleOne(t, root, amd64)
	m, err := Assemble(t.Context(), Spec{Base: base, Platform: amd64, Config: Config{
		Entrypoint: &[]string{}, ExposedPorts: &[]string{"8080/tcp"}, StopSignal: pointer("SIGQUIT"),
	}}, filepath.Join(root, "over"))
	require.NoError(t, err)
	cfg := readConfig(t, m)
	assert.Nil(t, cfg.Config.Entrypoint, "an empty list clears the base's")
	assert.Contains(t, cfg.Config.ExposedPorts, "8080/tcp")
	assert.Equal(t, "SIGQUIT", cfg.Config.StopSignal)
}

func pointer[T any](v T) *T { return &v }

func TestAttachReferrersWritesANewLayoutAndKeepsEveryBlob(t *testing.T) {
	root := t.TempDir()
	idx := assembleIndex(t, root)
	before, err := os.ReadFile(filepath.Join(idx, "index.json"))
	require.NoError(t, err)

	sbom, err := WriteSBOM(t.Context(), idx, filepath.Join(root, "sbom.spdx.json"))
	require.NoError(t, err)
	prov, err := WriteProvenance(t.Context(), idx, Provenance{BuilderID: "https://ci.example/build", SourceRevision: "abc"}, filepath.Join(root, "prov.json"))
	require.NoError(t, err)
	out, err := AttachReferrers(t.Context(), idx, []Document{sbom, prov}, filepath.Join(root, "attached"))
	require.NoError(t, err)

	after, err := os.ReadFile(filepath.Join(idx, "index.json"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "the subject layout is never edited")

	opened, err := OpenLayout(t.Context(), out.Path)
	require.NoError(t, err)
	require.Len(t, opened.Referrers, 2)
	for _, p := range []Platform{amd64, {OS: "linux", Architecture: "arm64"}} {
		m, err := opened.ResolvePlatform(t.Context(), p)
		require.NoError(t, err)
		for _, l := range m.Layers {
			assert.FileExists(t, blobPath(out.Path, l.Digest), "every layer the image reaches is carried over")
		}
	}
	data, err := os.ReadFile(blobPath(out.Path, opened.Referrers[0].Digest))
	require.NoError(t, err)
	var ref ocispec.Manifest
	require.NoError(t, json.Unmarshal(data, &ref))
	assert.Equal(t, ocispec.MediaTypeEmptyJSON, ref.Config.MediaType)
	require.NotNil(t, ref.Subject)
	assert.Equal(t, opened.Descriptor.Digest, ref.Subject.Digest)
	assert.Equal(t, MediaTypeSPDX, ref.Layers[0].MediaType)
	assert.Equal(t, MediaTypeSPDX, ref.ArtifactType)
}

func TestAttachReferrersRefusesADocumentChangedSinceWritten(t *testing.T) {
	root := t.TempDir()
	l := assembleOne(t, root, amd64)
	sbom, err := WriteSBOM(t.Context(), l, filepath.Join(root, "sbom.json"))
	require.NoError(t, err)
	write(t, sbom.Path, "tampered", 0o644)
	_, err = AttachReferrers(t.Context(), l, []Document{sbom}, filepath.Join(root, "out"))
	require.ErrorContains(t, err, "changed since it was written")
}

func TestDocumentDigestCoversTheBytesOnDisk(t *testing.T) {
	root := t.TempDir()
	l := assembleOne(t, root, amd64)
	for _, doc := range []func() (Document, error){
		func() (Document, error) { return WriteSBOM(t.Context(), l, filepath.Join(root, "sbom.json")) },
		func() (Document, error) {
			return WriteProvenance(t.Context(), l, Provenance{BuilderID: "b", SourceRevision: "r"}, filepath.Join(root, "prov.json"))
		},
	} {
		d, err := doc()
		require.NoError(t, err)
		data, err := os.ReadFile(d.Path)
		require.NoError(t, err)
		sum := sha256.Sum256(data)
		assert.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), d.Descriptor.Digest.String())
		assert.Equal(t, int64(len(data)), d.Descriptor.Size)
	}
}

func TestProvenanceNamesTheDigestAndAddsTheBase(t *testing.T) {
	root := t.TempDir()
	base := assembleOne(t, root, amd64)
	over, err := Assemble(t.Context(), Spec{Base: base, Platform: amd64}, filepath.Join(root, "over"))
	require.NoError(t, err)
	doc, err := WriteProvenance(t.Context(), over.Path, Provenance{BuilderID: "b", SourceRevision: "r"}, filepath.Join(root, "prov.json"))
	require.NoError(t, err)
	data, err := os.ReadFile(doc.Path)
	require.NoError(t, err)
	var st struct {
		Type    string `json:"_type"`
		Subject []struct {
			Name string `json:"name"`
		} `json:"subject"`
		Predicate struct {
			BuildDefinition struct {
				ResolvedDependencies []struct {
					Digest map[string]string `json:"digest"`
				} `json:"resolvedDependencies"`
			} `json:"buildDefinition"`
		} `json:"predicate"`
	}
	require.NoError(t, json.Unmarshal(data, &st))
	assert.Equal(t, "https://in-toto.io/Statement/v1", st.Type)
	assert.Equal(t, over.Descriptor.Digest.String(), st.Subject[0].Name, "a digest, never a host path")
	baseLayout, err := OpenLayout(t.Context(), base)
	require.NoError(t, err)
	require.Len(t, st.Predicate.BuildDefinition.ResolvedDependencies, 1)
	assert.Equal(t, baseLayout.Descriptor.Digest.Encoded(), st.Predicate.BuildDefinition.ResolvedDependencies[0].Digest["sha256"])
}

func TestAssembleIndexRefusesTheSamePlatformTwiceOnceNormalized(t *testing.T) {
	root := t.TempDir()
	a := assembleOne(t, filepath.Join(root, "a"), Platform{OS: "linux", Architecture: "arm64"})
	b := assembleOne(t, filepath.Join(root, "b"), Platform{OS: "linux", Architecture: "arm64", Variant: "v8"})
	_, err := AssembleIndex(t.Context(), []string{a, b}, filepath.Join(root, "idx"))
	require.ErrorContains(t, err, "are both")
}

func TestVerifyReportsADigestMismatch(t *testing.T) {
	desc := ocispec.Descriptor{Digest: digest.FromString("a"), Size: 1}
	require.ErrorIs(t, Verify(t.Context(), strings.NewReader("b"), desc), oci.ErrBlobDigest)
	require.NoError(t, Verify(t.Context(), strings.NewReader("a"), desc))
}

func BenchmarkAssembleLayer(b *testing.B) {
	root := b.TempDir()
	data := make([]byte, 16<<20)
	for i := range data {
		data[i] = byte(i * 7)
	}
	src := filepath.Join(root, "blob")
	require.NoError(b, os.WriteFile(src, data, 0o644))
	spec := Spec{Scratch: true, Platform: amd64, Files: []File{{Source: src, Path: "/blob", Mode: ModeFile}}}
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := Assemble(b.Context(), spec, filepath.Join(root, "out")); err != nil {
			b.Fatal(err)
		}
	}
}
