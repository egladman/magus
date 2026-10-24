package image

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func layerSource(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func buildLayer(t *testing.T, spec Spec) ([]byte, layerBlob) {
	t.Helper()
	var buf bytes.Buffer
	blob, err := writeLayer(context.Background(), &buf, spec, maxLayerBytes)
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), blob
}

type tarEntry struct {
	Header tar.Header
	Body   string
}

func readLayer(t *testing.T, gzipped []byte) (entries []tarEntry, tarBytes []byte) {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(gzipped))
	if err != nil {
		t.Fatal(err)
	}
	tarBytes, err = io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(tarBytes))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return entries, tarBytes
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, tarEntry{Header: *h, Body: string(body)})
	}
}

// R5, R10, R11: full header of each entry type, implicit parents, sort order.
func TestImageLayerEntryHeaders(t *testing.T) {
	src := layerSource(t, "hello")
	spec := Spec{
		Files: []File{
			{Source: src, Destination: "/usr/bin/magus", Mode: 0o4755, UID: 65532, GID: 65533},
			{Source: src, Destination: "/etc/conf", Mode: 0o644},
		},
		Symlinks: []Symlink{{Destination: "/usr/bin/m", Target: "magus", UID: 7, GID: 8}},
	}
	gz, _ := buildLayer(t, spec)
	got, _ := readLayer(t, gz)

	epoch := time.Unix(0, 0)
	want := []tar.Header{
		{Typeflag: tar.TypeDir, Name: "etc/", Mode: 0o755, Format: tar.FormatUSTAR},
		{Typeflag: tar.TypeReg, Name: "etc/conf", Size: 5, Mode: 0o644, Format: tar.FormatUSTAR},
		{Typeflag: tar.TypeDir, Name: "usr/", Mode: 0o755, Format: tar.FormatUSTAR},
		{Typeflag: tar.TypeDir, Name: "usr/bin/", Mode: 0o755, Format: tar.FormatUSTAR},
		{Typeflag: tar.TypeSymlink, Name: "usr/bin/m", Linkname: "magus", Mode: 0o777, Uid: 7, Gid: 8, Format: tar.FormatUSTAR},
		{Typeflag: tar.TypeReg, Name: "usr/bin/magus", Size: 5, Mode: 0o4755, Uid: 65532, Gid: 65533, Format: tar.FormatUSTAR},
	}
	if len(got) != len(want) {
		t.Fatalf("entries: got %d, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i].Header
		w.ModTime = epoch
		if !g.ModTime.Equal(w.ModTime) || !g.AccessTime.IsZero() || !g.ChangeTime.IsZero() {
			t.Errorf("%s: times mod=%v access=%v change=%v", w.Name, g.ModTime, g.AccessTime, g.ChangeTime)
		}
		if g.Uname != "" || g.Gname != "" || len(g.PAXRecords) != 0 || len(g.Xattrs) != 0 {
			t.Errorf("%s: owner names or records set: %+v", w.Name, g)
		}
		if g.Typeflag != w.Typeflag || g.Name != w.Name || g.Linkname != w.Linkname || g.Size != w.Size ||
			g.Mode != w.Mode || g.Uid != w.Uid || g.Gid != w.Gid || g.Format != w.Format {
			t.Errorf("entry %d: got %+v, want %+v", i, g, w)
		}
	}
	if got[1].Body != "hello" || got[5].Body != "hello" {
		t.Errorf("bodies: %q %q", got[1].Body, got[5].Body)
	}
}

// R10: byte order, with a sibling whose name sorts before '/'.
func TestImageLayerSortOrder(t *testing.T) {
	src := layerSource(t, "x")
	spec := Spec{Files: []File{
		{Source: src, Destination: "/b", Mode: 0o644},
		{Source: src, Destination: "/a/z", Mode: 0o644},
		{Source: src, Destination: "/a-b", Mode: 0o644},
		{Source: src, Destination: "/a/b", Mode: 0o644},
	}}
	gz, _ := buildLayer(t, spec)
	got, _ := readLayer(t, gz)
	var names []string
	for _, e := range got {
		names = append(names, e.Header.Name)
	}
	want := []string{"a/", "a-b", "a/b", "a/z", "b"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("order: got %q, want %q", names, want)
	}
}

// R11: a name USTAR cannot hold moves that entry to a PAX path record; its
// neighbors stay plain USTAR.
func TestImageLayerLongNameUsesPAX(t *testing.T) {
	src := layerSource(t, "x")
	long := "/" + strings.Repeat("f", 120)
	gz, _ := buildLayer(t, Spec{Files: []File{
		{Source: src, Destination: long, Mode: 0o644},
		{Source: src, Destination: "/short", Mode: 0o644},
	}})
	got, _ := readLayer(t, gz)
	if len(got) != 2 {
		t.Fatalf("entries: %+v", got)
	}
	file := got[0].Header
	if file.Name != long[1:] || file.Format != tar.FormatPAX || file.PAXRecords["path"] != long[1:] || len(file.PAXRecords) != 1 {
		t.Errorf("long file: name %q format %v records %v", file.Name, file.Format, file.PAXRecords)
	}
	if got[1].Header.Format != tar.FormatUSTAR || len(got[1].Header.PAXRecords) != 0 {
		t.Errorf("short file: format %v records %v", got[1].Header.Format, got[1].Header.PAXRecords)
	}
}

// R12, R13: DiffID is over the tar, the descriptor over the gzip bytes.
func TestImageLayerDigests(t *testing.T) {
	src := layerSource(t, strings.Repeat("compressible ", 200))
	gz, blob := buildLayer(t, Spec{Files: []File{{Source: src, Destination: "/f", Mode: 0o644}}})
	_, tarBytes := readLayer(t, gz)

	if want := digest.FromBytes(tarBytes); blob.diffID != want {
		t.Errorf("diffID: got %s, want %s", blob.diffID, want)
	}
	if want := digest.FromBytes(gz); blob.descriptor.Digest != want {
		t.Errorf("blob digest: got %s, want %s", blob.descriptor.Digest, want)
	}
	if blob.descriptor.Size != int64(len(gz)) {
		t.Errorf("size: got %d, want %d", blob.descriptor.Size, len(gz))
	}
	if blob.descriptor.MediaType != ocispec.MediaTypeImageLayerGzip {
		t.Errorf("media type: %s", blob.descriptor.MediaType)
	}
	if blob.diffID == blob.descriptor.Digest {
		t.Error("diffID equals blob digest")
	}
}

// D3: gzip header mtime 0, no name, no comment; entry mtime Unix 0.
func TestImageLayerGzipHeader(t *testing.T) {
	src := layerSource(t, "x")
	if err := os.Chtimes(src, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	gz, _ := buildLayer(t, Spec{Files: []File{{Source: src, Destination: "/f", Mode: 0o644}}})
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	h := zr.Header
	if !h.ModTime.IsZero() && h.ModTime.Unix() != 0 || h.Name != "" || h.Comment != "" || len(h.Extra) != 0 {
		t.Errorf("gzip header: %+v", h)
	}
	if want := []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 2, 255}; !bytes.Equal(gz[:10], want) {
		t.Errorf("gzip header bytes: got % x, want % x", gz[:10], want)
	}
	entries, _ := readLayer(t, gz)
	for _, e := range entries {
		if e.Header.ModTime.Unix() != 0 {
			t.Errorf("%s: mtime %v", e.Header.Name, e.Header.ModTime)
		}
	}
}

// R31 for the layer: creation order, host mtimes and source paths do not show.
func TestImageLayerIsDeterministic(t *testing.T) {
	build := func(order []int, mtime time.Time) ([]byte, layerBlob) {
		dir := t.TempDir()
		names := []string{"one", "two", "three"}
		var files []File
		for _, i := range order {
			p := filepath.Join(dir, names[i])
			if err := os.WriteFile(p, []byte(names[i]), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(p, mtime, mtime); err != nil {
				t.Fatal(err)
			}
			files = append(files, File{Source: p, Destination: "/app/" + names[i], Mode: 0o644})
		}
		return buildLayer(t, Spec{Files: files})
	}
	a, ab := build([]int{0, 1, 2}, time.Unix(1_000_000, 0))
	b, bb := build([]int{2, 0, 1}, time.Unix(2_000_000_000, 0))
	if !bytes.Equal(a, b) || !reflect.DeepEqual(ab, bb) {
		t.Fatalf("layers differ: %s vs %s", ab.descriptor.Digest, bb.descriptor.Digest)
	}
}

func TestImageLayerSensitivity(t *testing.T) {
	base := func() Spec {
		return Spec{
			Files:    []File{{Source: layerSource(t, "v1"), Destination: "/f", Mode: 0o644, UID: 1, GID: 1}},
			Symlinks: []Symlink{{Destination: "/l", Target: "f"}},
		}
	}
	_, ref := buildLayer(t, base())
	cases := map[string]func(*Spec){
		"mode":           func(s *Spec) { s.Files[0].Mode = 0o755 },
		"uid":            func(s *Spec) { s.Files[0].UID = 2 },
		"gid":            func(s *Spec) { s.Files[0].GID = 2 },
		"file bytes":     func(s *Spec) { s.Files[0].Source = layerSource(t, "v2") },
		"symlink target": func(s *Spec) { s.Symlinks[0].Target = "g" },
	}
	for name, mutate := range cases {
		s := base()
		mutate(&s)
		_, got := buildLayer(t, s)
		if got.diffID == ref.diffID || got.descriptor.Digest == ref.descriptor.Digest {
			t.Errorf("%s: layer digest unchanged", name)
		}
	}
}

// R9: nothing to place means no layer, and the caller must not ask for one.
func TestImageLayerEmptySpecIsRefused(t *testing.T) {
	var buf bytes.Buffer
	_, err := writeLayer(context.Background(), &buf, Spec{Env: map[string]string{"A": "b"}}, maxLayerBytes)
	if err == nil || !strings.Contains(err.Error(), "no files or symlinks") {
		t.Fatalf("error: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes", buf.Len())
	}
}

// R6: only a regular file is a source.
func TestImageLayerSourceMustBeRegularFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "real")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{"directory": dir, "symlink": link, "missing": filepath.Join(dir, "none")} {
		var buf bytes.Buffer
		_, err := writeLayer(context.Background(), &buf, Spec{Files: []File{{Source: src, Destination: "/f", Mode: 0o644}}}, maxLayerBytes)
		if err == nil || !strings.Contains(err.Error(), "source") || !strings.Contains(err.Error(), src) {
			t.Errorf("%s: error %v", name, err)
		}
	}
}

func TestImageLayerNegativeOwnerIsRefused(t *testing.T) {
	var buf bytes.Buffer
	_, err := writeLayer(context.Background(), &buf, Spec{Files: []File{{Source: layerSource(t, "x"), Destination: "/f", UID: -1}}}, maxLayerBytes)
	if err == nil || !strings.Contains(err.Error(), "uid") {
		t.Fatalf("error: %v", err)
	}
}

// R14: the cap counts uncompressed bytes, at the write that crosses it.
func TestImageLayerSizeCap(t *testing.T) {
	src := layerSource(t, strings.Repeat("a", 4096))
	spec := Spec{Files: []File{{Source: src, Destination: "/f", Mode: 0o644}}}

	var ok bytes.Buffer
	if _, err := writeLayer(context.Background(), &ok, spec, 1<<20); err != nil {
		t.Fatalf("under the cap: %v", err)
	}
	var over bytes.Buffer
	_, err := writeLayer(context.Background(), &over, spec, 2048)
	if err == nil || !strings.Contains(err.Error(), "uncompressed size exceeds 2048") {
		t.Fatalf("over the cap: %v", err)
	}
}

// R14: a canceled context stops the write before any source is read.
func TestImageLayerCancellation(t *testing.T) {
	src := layerSource(t, "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buf bytes.Buffer
	_, err := writeLayer(ctx, &buf, Spec{Files: []File{{Source: src, Destination: "/f", Mode: 0o644}}}, maxLayerBytes)
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("error: %v", err)
	}
}

func TestImageLayerCancellationDuringCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &ctxReader{ctx: ctx, r: strings.NewReader("abc")}
	buf := make([]byte, 1)
	if _, err := r.Read(buf); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := r.Read(buf); err != context.Canceled {
		t.Fatalf("read after cancel: %v", err)
	}
}
