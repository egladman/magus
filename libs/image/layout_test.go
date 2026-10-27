package image

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const testManifestType = "application/vnd.oci.image.manifest.v1+json"

func testRoot(t *testing.T) (ocispec.Descriptor, []blob) {
	t.Helper()
	manifest := bytesBlob(testManifestType, []byte(`{"schemaVersion":2}`))
	layer := bytesBlob("application/vnd.oci.image.layer.v1.tar+gzip", []byte("layer bytes"))
	root := manifest.desc
	root.Platform = &ocispec.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}
	return root, []blob{manifest, layer}
}

// snapshot maps every path under dir to its mode and content.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		rel, _ := filepath.Rel(dir, p)
		info, err := d.Info()
		require.NoError(t, err)
		body := ""
		if d.Type().IsRegular() {
			b, err := os.ReadFile(p)
			require.NoError(t, err)
			body = string(b)
		}
		out[rel] = fmt.Sprintf("%s|%s", info.Mode(), body)
		return nil
	}))
	return out
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestImageLayoutWriteThenOpen(t *testing.T) {
	root, blobs := testRoot(t)
	dest := filepath.Join(t.TempDir(), "out")

	require.NoError(t, writeLayout(context.Background(), dest, root, blobs))

	assert.Equal(t, `{"imageLayoutVersion":"1.0.0"}`, snapshotBody(t, filepath.Join(dest, "oci-layout")))
	assert.JSONEq(t,
		fmt.Sprintf(`{"schemaVersion":2,"manifests":[{"mediaType":%q,"digest":%q,"size":19,"platform":{"architecture":"arm64","os":"linux","variant":"v8"}}]}`, testManifestType, root.Digest),
		snapshotBody(t, filepath.Join(dest, "index.json")))
	assert.ElementsMatch(t,
		[]string{blobs[0].desc.Digest.Encoded(), blobs[1].desc.Digest.Encoded()},
		entries(t, filepath.Join(dest, "blobs", "sha256")))

	got, err := OpenLayout(dest)
	require.NoError(t, err)
	assert.Equal(t, Layout{
		Path:       dest,
		Descriptor: root,
		Platform:   Platform{OS: "linux", Architecture: "arm64", Variant: "v8"},
	}, got)
}

func snapshotBody(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func TestImageLayoutModesIgnoreUmask(t *testing.T) {
	root, blobs := testRoot(t)
	dest := filepath.Join(t.TempDir(), "out")
	require.NoError(t, writeLayout(context.Background(), dest, root, blobs))

	for p, v := range snapshot(t, dest) {
		info, err := os.Stat(filepath.Join(dest, p))
		require.NoError(t, err)
		want := fs.FileMode(0o644)
		if info.IsDir() {
			want = 0o755
		}
		assert.Equal(t, want, info.Mode().Perm(), "path %q (%s)", p, v)
	}
}

func TestImageLayoutDuplicateBlobWrittenOnce(t *testing.T) {
	root, blobs := testRoot(t)
	dest := filepath.Join(t.TempDir(), "out")
	require.NoError(t, writeLayout(context.Background(), dest, root, append(blobs, blobs[1])))
	assert.Len(t, entries(t, filepath.Join(dest, "blobs", "sha256")), 2)
}

func TestImageLayoutFailedWriteLeavesDestIdentical(t *testing.T) {
	root, blobs := testRoot(t)
	parent := t.TempDir()
	dest := filepath.Join(parent, "out")
	require.NoError(t, writeLayout(context.Background(), dest, root, blobs))
	before := snapshot(t, dest)

	liar := bytesBlob("application/octet-stream", []byte("actual"))
	liar.desc.Digest = digest.FromString("something else")
	other, otherBlobs := testRoot(t)
	other.Size = 19

	cases := map[string][]blob{
		"wrong digest": append(append([]blob{}, otherBlobs...), liar),
		"wrong size": append(append([]blob{}, otherBlobs...), func() blob {
			b := bytesBlob("application/octet-stream", []byte("actual"))
			b.desc.Size++
			return b
		}()),
		"unopenable": append(append([]blob{}, otherBlobs...), blob{
			desc: bytesBlob("application/octet-stream", []byte("x")).desc,
			open: func() (io.ReadCloser, error) { return nil, os.ErrPermission },
		}),
	}
	for name, bs := range cases {
		t.Run(name, func(t *testing.T) {
			err := writeLayout(context.Background(), dest, other, bs)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "image.layout: blob")
			assert.Equal(t, before, snapshot(t, dest))
			assert.Equal(t, []string{"out"}, entries(t, parent), "no staging directory remains")
		})
	}
}

func TestImageLayoutFailedFirstWriteCreatesNothing(t *testing.T) {
	root, blobs := testRoot(t)
	blobs[1].desc.Size++
	parent := t.TempDir()

	err := writeLayout(context.Background(), filepath.Join(parent, "out"), root, blobs)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "size is")
	assert.Empty(t, entries(t, parent))
}

func TestImageLayoutCancelledWriteLeavesDestIdentical(t *testing.T) {
	root, blobs := testRoot(t)
	parent := t.TempDir()
	dest := filepath.Join(parent, "out")
	require.NoError(t, writeLayout(context.Background(), dest, root, blobs))
	before := snapshot(t, dest)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := writeLayout(ctx, dest, root, blobs)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, before, snapshot(t, dest))
	assert.Equal(t, []string{"out"}, entries(t, parent))
}

func TestImageLayoutOverwriteReplacesWholeTree(t *testing.T) {
	root, blobs := testRoot(t)
	parent := t.TempDir()
	dest := filepath.Join(parent, "out")
	require.NoError(t, os.MkdirAll(filepath.Join(dest, "stale"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "stale", "f"), []byte("old"), 0o644))

	require.NoError(t, writeLayout(context.Background(), dest, root, blobs))

	assert.ElementsMatch(t, []string{"oci-layout", "index.json", "blobs"}, entries(t, dest))
	assert.Equal(t, []string{"out"}, entries(t, parent))
}

func TestImageLayoutRootMustBeAmongBlobs(t *testing.T) {
	root, blobs := testRoot(t)
	parent := t.TempDir()

	err := writeLayout(context.Background(), filepath.Join(parent, "out"), root, blobs[1:])

	require.Error(t, err)
	assert.Contains(t, err.Error(), "root "+root.Digest.String())
	assert.Empty(t, entries(t, parent))
}

func TestImageLayoutRefusesNonSHA256Digest(t *testing.T) {
	root, blobs := testRoot(t)
	root.Digest = "sha512:abc"

	err := writeLayout(context.Background(), filepath.Join(t.TempDir(), "out"), root, blobs)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a sha256 digest")
}

func TestImageLayoutOpenRefusesCorruptedBlob(t *testing.T) {
	cases := map[string]struct {
		mutate func([]byte) []byte
		want   string
	}{
		"flipped byte": {func(b []byte) []byte { b[0] ^= 0xff; return b }, "content hashes to"},
		"truncated":    {func(b []byte) []byte { return b[:len(b)-1] }, "size is"},
		"extended":     {func(b []byte) []byte { return append(b, 'x') }, "size is"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root, blobs := testRoot(t)
			dest := filepath.Join(t.TempDir(), "out")
			require.NoError(t, writeLayout(context.Background(), dest, root, blobs))
			p := blobPath(dest, root.Digest)
			require.NoError(t, os.WriteFile(p, tc.mutate(snapshotBytes(t, p)), 0o644))

			_, err := OpenLayout(dest)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "image.open")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func snapshotBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func TestImageLayoutOpenRefusesMissingBlob(t *testing.T) {
	root, blobs := testRoot(t)
	dest := filepath.Join(t.TempDir(), "out")
	require.NoError(t, writeLayout(context.Background(), dest, root, blobs))
	require.NoError(t, os.Remove(blobPath(dest, root.Digest)))

	_, err := OpenLayout(dest)

	require.Error(t, err)
	assert.Contains(t, err.Error(), root.Digest.String())
}

func TestImageLayoutOpenRefusesWrongDescriptorCount(t *testing.T) {
	root, blobs := testRoot(t)
	for name, index := range map[string]string{
		"none": `{"schemaVersion":2,"manifests":[]}`,
		"two":  fmt.Sprintf(`{"schemaVersion":2,"manifests":[{"mediaType":%q,"digest":%q,"size":19},{"mediaType":%q,"digest":%q,"size":19}]}`, testManifestType, root.Digest, testManifestType, root.Digest),
	} {
		t.Run(name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "out")
			require.NoError(t, writeLayout(context.Background(), dest, root, blobs))
			require.NoError(t, os.WriteFile(filepath.Join(dest, "index.json"), []byte(index), 0o644))

			_, err := OpenLayout(dest)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "exactly 1")
		})
	}
}

func TestImageLayoutOpenRefusesBadMarker(t *testing.T) {
	root, blobs := testRoot(t)
	dest := filepath.Join(t.TempDir(), "out")
	require.NoError(t, writeLayout(context.Background(), dest, root, blobs))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "oci-layout"), []byte(`{"imageLayoutVersion":"2.0.0"}`), 0o644))

	_, err := OpenLayout(dest)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "oci-layout version")
}

func TestImageLayoutBlobReusedFromAnotherLayout(t *testing.T) {
	root, blobs := testRoot(t)
	src := filepath.Join(t.TempDir(), "src")
	require.NoError(t, writeLayout(context.Background(), src, root, blobs))

	dest := filepath.Join(t.TempDir(), "dest")
	reused := []blob{layoutBlob(src, blobs[0].desc), layoutBlob(src, blobs[1].desc)}
	require.NoError(t, writeLayout(context.Background(), dest, root, reused))

	assert.Equal(t, snapshot(t, src), snapshot(t, dest))
}

func TestImageLayoutDiskBlobHardlinks(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	payload := []byte("hardlink-me")
	require.NoError(t, os.WriteFile(src, payload, 0o600))
	desc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(payload),
		Size:      int64(len(payload)),
	}
	root := desc
	dest := filepath.Join(dir, "layout")
	require.NoError(t, writeLayout(context.Background(), dest, root, []blob{{
		desc: desc,
		path: src,
		open: func() (io.ReadCloser, error) { return os.Open(src) },
	}}))

	installed := blobPath(dest, desc.Digest)
	srcInfo, err := os.Stat(src)
	require.NoError(t, err)
	dstInfo, err := os.Stat(installed)
	require.NoError(t, err)
	assert.True(t, os.SameFile(srcInfo, dstInfo), "same-fs install must hardlink, not copy")
	assert.Equal(t, os.FileMode(0o644), dstInfo.Mode().Perm())
}

// layerPayloadSize is large enough that link vs stream shows wall-clock and
// alloc delta; small enough for a unit-test sandbox.
const layerPayloadSize = 4 << 20 // 4 MiB

func benchBlob(size int) (blob, []byte) {
	data := bytes.Repeat([]byte("m"), size)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	return blob{
		desc: desc,
		open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(data)), nil
		},
	}, data
}

func BenchmarkWriteBlobStream(b *testing.B) {
	bl, _ := benchBlob(layerPayloadSize)
	dir := b.TempDir()
	b.SetBytes(int64(layerPayloadSize))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := filepath.Join(dir, bl.desc.Digest.Encoded()+".stream")
		if err := writeBlob(target, bl); err != nil {
			b.Fatal(err)
		}
		_ = os.Remove(target)
	}
}

func BenchmarkWriteBlobDiskLink(b *testing.B) {
	_, data := benchBlob(layerPayloadSize)
	dir := b.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		b.Fatal(err)
	}
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	bl := blob{
		desc: desc,
		path: src,
		open: func() (io.ReadCloser, error) { return os.Open(src) },
	}
	b.SetBytes(int64(layerPayloadSize))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := filepath.Join(dir, desc.Digest.Encoded()+".link")
		if err := writeBlob(target, bl); err != nil {
			b.Fatal(err)
		}
		_ = os.Remove(target)
	}
}

func BenchmarkWriteLayoutReuse(b *testing.B) {
	root, blobs := func() (ocispec.Descriptor, []blob) {
		data := bytes.Repeat([]byte("x"), layerPayloadSize)
		layer := bytesBlob(ocispec.MediaTypeImageLayerGzip, data)
		man := bytesBlob(testManifestType, []byte(`{"schemaVersion":2}`))
		return man.desc, []blob{man, layer}
	}()
	src := filepath.Join(b.TempDir(), "src")
	if err := writeLayout(context.Background(), src, root, blobs); err != nil {
		b.Fatal(err)
	}
	reused := []blob{layoutBlob(src, blobs[0].desc), layoutBlob(src, blobs[1].desc)}
	b.SetBytes(int64(layerPayloadSize))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dest := filepath.Join(b.TempDir(), "dest")
		if err := writeLayout(context.Background(), dest, root, reused); err != nil {
			b.Fatal(err)
		}
	}
}
