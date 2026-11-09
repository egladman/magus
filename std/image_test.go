//go:build !wasm

package std

import (
	"github.com/egladman/magus/types/image"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

var linuxAMD64 = image.Platform{OS: image.OSLinux, Architecture: image.ArchitectureAMD64}

func TestImageAssembleWritesNothingWhileTracing(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	_, err := ImageAssemble(types.WithTrace(t.Context()), image.Spec{Scratch: true, Platform: linuxAMD64}, dest)
	require.NoError(t, err)
	assert.NoDirExists(t, dest, "a dry run traces the assemble and writes nothing")
}

func TestImageAssembleNamesFsModeForAZeroMode(t *testing.T) {
	src := filepath.Join(t.TempDir(), "f")
	require.NoError(t, os.WriteFile(src, []byte("x"), 0o644))
	_, err := ImageAssemble(t.Context(), image.Spec{Scratch: true, Platform: linuxAMD64,
		Files: []image.File{{Source: src, Path: "/f"}}}, filepath.Join(t.TempDir(), "out"))
	require.ErrorContains(t, err, `fs\mode("0644")`)
}

func TestImageAssembleRefusesAnUnsetPlatform(t *testing.T) {
	_, err := ImageAssemble(t.Context(), image.Spec{Scratch: true}, filepath.Join(t.TempDir(), "out"))
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
	got, err := ImageAssemble(t.Context(), image.Spec{Scratch: true, Platform: linuxAMD64,
		Files:  []image.File{{Source: src, Path: "/app", Mode: 0o755}},
		Config: image.Config{Entrypoint: &cmd}}, filepath.Join(root, "out"))
	require.NoError(t, err)
	assert.Equal(t, "sha256", got.Descriptor.Digest.Algorithm)
	assert.Equal(t, string(image.MediaTypeManifest), got.Descriptor.MediaType)
	require.NotNil(t, got.Added)
	assert.Equal(t, 1, got.Added.Files)

	opened, err := ImageOpen(t.Context(), got.Path, "")
	require.NoError(t, err)
	assert.Equal(t, got.Descriptor.Digest, opened.Descriptor.Digest)
	require.NotNil(t, opened.Platform)
	assert.Equal(t, linuxAMD64, *opened.Platform)
}

func TestImageDigestString(t *testing.T) {
	assert.Equal(t, "sha256:abc", image.Digest{Algorithm: "sha256", Encoded: "abc"}.String())
	assert.Empty(t, image.Digest{}.String())
}

func (s *stdSuite) TestParsePlatformTakesAnAMD64VariantThatSetsGOAMD64() {
	p, err := ImageParsePlatform(s.T().Context(), "linux/amd64/v3")
	s.Require().NoError(err)
	s.Equal(image.Platform{OS: image.OSLinux, Architecture: image.ArchitectureAMD64, Variant: image.VariantV3}, p)

	env, err := ImageGoEnv(s.T().Context(), p)
	s.Require().NoError(err)
	s.Equal(map[string]string{"GOOS": "linux", "GOARCH": "amd64", "GOAMD64": "v3"}, env)
}

func (s *stdSuite) TestOpenRefusesANameTheLayoutDoesNotHold() {
	got, err := ImageAssemble(s.T().Context(), image.Spec{Scratch: true, Platform: linuxAMD64}, filepath.Join(s.T().TempDir(), "out"))
	s.Require().NoError(err)

	_, err = ImageOpen(s.T().Context(), got.Path, "registry.example/team/app:v1")
	s.Require().Error(err)
}

func (s *stdSuite) appSpec() image.Spec {
	src := filepath.Join(s.T().TempDir(), "app")
	s.Require().NoError(os.WriteFile(src, []byte("binary"), 0o755))
	return image.Spec{Scratch: true, Platform: linuxAMD64,
		Files: []image.File{{Source: src, Path: "/usr/bin/app", Mode: 0o755, UID: 65532, GID: 65532}}}
}

func (s *stdSuite) TestLayoutListsEveryEntryWithNoDigest() {
	got, err := ImageLayout(s.T().Context(), s.appSpec())
	s.Require().NoError(err)
	epoch := "1970-01-01T00:00:00Z"
	s.Equal(image.Layout{Platform: &linuxAMD64, Added: &image.AddedLayer{
		Entries: []image.Entry{
			{Path: "/usr", Kind: image.EntryKindDirectory, Mode: "0755", ModTime: epoch, Origin: "parent"},
			{Path: "/usr/bin", Kind: image.EntryKindDirectory, Mode: "0755", ModTime: epoch, Origin: "parent"},
			{Path: "/usr/bin/app", Kind: image.EntryKindFile, Mode: "0755", UID: 65532, GID: 65532, Size: 6, ModTime: epoch, Origin: "files[0]"},
		},
		Files: 1, Directories: 2,
	}}, got)
}

func (s *stdSuite) TestAssembleUnderADryRunReturnsTheLayoutAndWritesNothing() {
	spec := s.appSpec()
	want, err := ImageLayout(s.T().Context(), spec)
	s.Require().NoError(err)
	dest := filepath.Join(s.T().TempDir(), "out")
	got, err := ImageAssemble(types.WithTrace(s.T().Context()), spec, dest)
	s.Require().NoError(err)
	s.Equal(want, got)
	s.NoDirExists(dest)
}

func (s *stdSuite) TestAssembleReceiptCarriesTheEntriesLayoutListed() {
	spec := s.appSpec()
	listed, err := ImageLayout(s.T().Context(), spec)
	s.Require().NoError(err)
	got, err := ImageAssemble(s.T().Context(), spec, filepath.Join(s.T().TempDir(), "out"))
	s.Require().NoError(err)
	s.Require().NotNil(got.Added)
	s.Equal(listed.Added.Entries, got.Added.Entries)
	s.NotEmpty(got.Added.DiffID.Encoded, "only assemble has digests")
}

// The two specs of std/examples/image/distroless.buzz, over the files its
// rootfs and build targets write.
func (s *stdSuite) TestLayoutOfTheDistrolessExample() {
	root := s.T().TempDir()
	for name, content := range map[string]string{
		"rootfs/etc/passwd":        "root:x:0:0:root:/root:/sbin/nologin\nnonroot:x:65532:65532:nonroot:/home/nonroot:/sbin/nologin\n",
		"rootfs/etc/group":         "root:x:0:\nnonroot:x:65532:\n",
		"rootfs/etc/nsswitch.conf": "hosts: files dns\n",
		"linux-amd64/app":          "app",
	} {
		p := filepath.Join(root, name)
		s.Require().NoError(os.MkdirAll(filepath.Dir(p), 0o755))
		s.Require().NoError(os.WriteFile(p, []byte(content), 0o644))
	}
	epoch := "1970-01-01T00:00:00Z"
	rootfs, err := ImageLayout(s.T().Context(), image.Spec{Scratch: true, Platform: linuxAMD64,
		Trees: []image.Tree{{Source: filepath.Join(root, "rootfs"), Path: "/", ModePolicy: image.ModePolicyFixed, Symlinks: image.SymlinkPolicyRefuse}},
		Directories: []image.Directory{
			{Path: "/tmp", Mode: 0o1777},
			{Path: "/home/nonroot", Mode: 0o700, UID: 65532, GID: 65532},
		}})
	s.Require().NoError(err)
	s.Require().NotNil(rootfs.Added)
	file := func(p string, size int) image.Entry {
		return image.Entry{Path: p, Kind: image.EntryKindFile, Mode: "0644", Size: size, ModTime: epoch, Origin: "trees[0] " + p[1:]}
	}
	dir := func(p, mode, origin string, owner int) image.Entry {
		return image.Entry{Path: p, Kind: image.EntryKindDirectory, Mode: mode, UID: owner, GID: owner, ModTime: epoch, Origin: origin}
	}
	s.Equal([]image.Entry{
		dir("/etc", "0755", "parent", 0),
		file("/etc/group", 27),
		file("/etc/nsswitch.conf", 17),
		file("/etc/passwd", 94),
		dir("/home", "0755", "parent", 0),
		dir("/home/nonroot", "0700", "directories[1]", 65532),
		dir("/tmp", "1777", "directories[0]", 0),
	}, rootfs.Added.Entries)

	app, err := ImageLayout(s.T().Context(), image.Spec{Base: filepath.Join(root, "image", "rootfs"), Platform: linuxAMD64,
		Files: []image.File{{Source: filepath.Join(root, "linux-amd64", "app"), Path: "/usr/local/bin/app", Mode: 0o755}}})
	s.Require().NoError(err)
	s.Require().NotNil(app.Added)
	s.Equal([]image.Entry{
		{Path: "/usr/local/bin/app", Kind: image.EntryKindFile, Mode: "0755", Size: 3, ModTime: epoch, Origin: "files[0]"},
	}, app.Added.Entries, "over a base, no parent is listed")
	for _, e := range slices.Concat(rootfs.Added.Entries, app.Added.Entries) {
		s.T().Logf("%-9s %s %d:%d %5d %s %-20s <- %s", e.Kind, e.Mode, e.UID, e.GID, e.Size, e.ModTime, e.Path, e.Origin)
	}
}

func (s *stdSuite) TestAssembleUnderADryRunNamesAMissingTreeSource() {
	missing := filepath.Join(s.T().TempDir(), "out", "rootfs")
	_, err := ImageAssemble(types.WithTrace(s.T().Context()), image.Spec{Scratch: true, Platform: linuxAMD64,
		Trees: []image.Tree{{Source: missing, Path: "/", ModePolicy: image.ModePolicyFixed, Symlinks: image.SymlinkPolicyRefuse}}},
		filepath.Join(s.T().TempDir(), "image"))
	s.Require().ErrorContains(err, "trees[0] "+missing)
}

func (s *stdSuite) attachedLayout() (image.Layout, image.Document) {
	ctx, root := s.T().Context(), s.T().TempDir()
	built, err := ImageAssemble(ctx, image.Spec{Scratch: true, Platform: linuxAMD64}, filepath.Join(root, "image"))
	s.Require().NoError(err)
	sbom, err := ImageWriteSBOM(ctx, built.Path, filepath.Join(root, "sbom.spdx.json"))
	s.Require().NoError(err)
	attached, err := ImageAttachReferrers(ctx, built.Path, []image.Document{sbom}, filepath.Join(root, "attached"))
	s.Require().NoError(err)
	return attached, sbom
}

func (s *stdSuite) TestReferrersReadsTheAttachedDocumentWithoutARegistry() {
	attached, sbom := s.attachedLayout()

	docs, err := ImageReferrers(s.T().Context(), attached.Path, "")
	s.Require().NoError(err)
	s.Require().Len(docs, 1)
	wantDescriptor := sbom.Descriptor
	wantDescriptor.ArtifactType = sbom.Descriptor.MediaType
	wantDescriptor.Annotations = map[string]string{"org.opencontainers.image.title": filepath.Base(sbom.Path)}
	s.Equal(wantDescriptor, docs[0].Descriptor)

	want, err := os.ReadFile(sbom.Path)
	s.Require().NoError(err)
	got, err := os.ReadFile(docs[0].Path)
	s.Require().NoError(err)
	s.Equal(want, got)
}

func (s *stdSuite) TestReferrersIsEmptyForALayoutWithNone() {
	built, err := ImageAssemble(s.T().Context(), image.Spec{Scratch: true, Platform: linuxAMD64}, filepath.Join(s.T().TempDir(), "image"))
	s.Require().NoError(err)

	docs, err := ImageReferrers(s.T().Context(), built.Path, "")
	s.Require().NoError(err)
	s.Empty(docs)
}

func (s *stdSuite) TestReferrersRefusesAMissingLayout() {
	_, err := ImageReferrers(s.T().Context(), filepath.Join(s.T().TempDir(), "absent"), "")
	s.Require().Error(err)
}

func (s *stdSuite) TestReferrersRefusesAReferrerBlobChangedOnDisk() {
	attached, _ := s.attachedLayout()
	opened, err := ImageOpen(s.T().Context(), attached.Path, "")
	s.Require().NoError(err)
	s.Require().Len(opened.Referrers, 1)
	d, err := digestFromTypes(opened.Referrers[0].Digest)
	s.Require().NoError(err)
	blob := layoutBlobPath(attached.Path, d)
	s.Require().NoError(os.Chmod(blob, 0o644))
	s.Require().NoError(os.WriteFile(blob, []byte("{}"), 0o644))

	_, err = ImageReferrers(s.T().Context(), attached.Path, "")
	s.Require().Error(err)
}

func TestFsModeParsesOctalAndRwx(t *testing.T) {
	got, err := FsMode(t.Context(), "rwxr-xr-x")
	require.NoError(t, err)
	assert.Equal(t, 0o755, got)
	_, err = FsMode(t.Context(), "0")
	require.Error(t, err)
}
