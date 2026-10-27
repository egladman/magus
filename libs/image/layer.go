package image

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// maxLayerBytes caps the uncompressed tar, matching the archive module's default.
const maxLayerBytes int64 = 10 << 30

// ustarNameMax is the longest name or link target a plain USTAR header holds
// without the prefix field.
const ustarNameMax = 100

type layerEntry struct {
	header tar.Header
	source string
}

type layerBlob struct {
	diffID     digest.Digest
	descriptor ocispec.Descriptor
}

func layerHeader(name string, typeflag byte, mode int64, uid, gid int, linkname string) tar.Header {
	format := tar.FormatUSTAR
	if len(name) > ustarNameMax || len(linkname) > ustarNameMax {
		format = tar.FormatPAX
	}
	return tar.Header{
		Typeflag: typeflag,
		Name:     name,
		Linkname: linkname,
		Mode:     mode,
		Uid:      uid,
		Gid:      gid,
		ModTime:  time.Unix(0, 0),
		Format:   format,
	}
}

// layerEntries lists the spec's entries in destination byte order, with every
// parent directory no entry declares added as a root-owned 0755 directory.
// Entries are keyed by destination, so the sort keeps a parent ahead of its
// children: the parent's path is a prefix of theirs.
func layerEntries(spec Spec) ([]layerEntry, error) {
	byDest := make(map[string]layerEntry, len(spec.Files)+len(spec.Symlinks)+len(spec.emptyDirs))
	for _, f := range spec.Files {
		if f.UID < 0 || f.GID < 0 {
			return nil, fmt.Errorf("image.layer: file %q: uid and gid must not be negative", f.Destination)
		}
		name := strings.TrimPrefix(f.Destination, "/")
		byDest[f.Destination] = layerEntry{
			header: layerHeader(name, tar.TypeReg, int64(f.Mode), f.UID, f.GID, ""),
			source: f.Source,
		}
	}
	for _, l := range spec.Symlinks {
		if l.UID < 0 || l.GID < 0 {
			return nil, fmt.Errorf("image.layer: symlink %q: uid and gid must not be negative", l.Destination)
		}
		name := strings.TrimPrefix(l.Destination, "/")
		byDest[l.Destination] = layerEntry{
			header: layerHeader(name, tar.TypeSymlink, 0o777, l.UID, l.GID, l.Target),
		}
	}
	for _, dir := range spec.emptyDirs {
		if _, ok := byDest[dir.Destination]; ok {
			continue
		}
		if dir.UID < 0 || dir.GID < 0 {
			return nil, fmt.Errorf("image.layer: dir %q: uid and gid must not be negative", dir.Destination)
		}
		name := strings.TrimPrefix(dir.Destination, "/") + "/"
		mode := int64(dir.Mode)
		if mode == 0 {
			mode = 0o755
		}
		byDest[dir.Destination] = layerEntry{
			header: layerHeader(name, tar.TypeDir, mode, dir.UID, dir.GID, ""),
		}
	}
	for dest := range byDest {
		for dir := path.Dir(dest); dir != "/"; dir = path.Dir(dir) {
			if _, ok := byDest[dir]; ok {
				continue
			}
			byDest[dir] = layerEntry{
				header: layerHeader(strings.TrimPrefix(dir, "/")+"/", tar.TypeDir, 0o755, 0, 0, ""),
			}
		}
	}
	dests := make([]string, 0, len(byDest))
	for dest := range byDest {
		dests = append(dests, dest)
	}
	sort.Strings(dests)
	entries := make([]layerEntry, len(dests))
	for i, dest := range dests {
		entries[i] = byDest[dest]
	}
	return entries, nil
}

// writeLayer writes the spec's one new layer to w as a deterministic tar+gzip
// and returns its DiffID (sha256 of the tar) and descriptor (sha256 and length
// of the bytes written to w). limit bounds the uncompressed tar.
func writeLayer(ctx context.Context, w io.Writer, spec Spec, limit int64) (layerBlob, error) {
	if len(spec.Files) == 0 && len(spec.Symlinks) == 0 {
		return layerBlob{}, fmt.Errorf("image.layer: no files or symlinks to place")
	}
	entries, err := layerEntries(spec)
	if err != nil {
		return layerBlob{}, err
	}

	blob := digest.Canonical.Digester()
	blobSize := &countWriter{}
	gz, err := gzip.NewWriterLevel(io.MultiWriter(w, blob.Hash(), blobSize), gzip.BestCompression)
	if err != nil {
		return layerBlob{}, fmt.Errorf("image.layer: gzip: %w", err)
	}
	gz.Header = gzip.Header{OS: 255}

	diff := digest.Canonical.Digester()
	tw := tar.NewWriter(&limitWriter{w: io.MultiWriter(diff.Hash(), gz), limit: limit})

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return layerBlob{}, fmt.Errorf("image.layer: %w", err)
		}
		if err := writeEntry(ctx, tw, e); err != nil {
			return layerBlob{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return layerBlob{}, fmt.Errorf("image.layer: tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return layerBlob{}, fmt.Errorf("image.layer: gzip: %w", err)
	}
	return layerBlob{
		diffID: diff.Digest(),
		descriptor: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageLayerGzip,
			Digest:    blob.Digest(),
			Size:      blobSize.n,
		},
	}, nil
}

func writeEntry(ctx context.Context, tw *tar.Writer, e layerEntry) error {
	hdr := e.header
	if hdr.Typeflag != tar.TypeReg {
		if err := tw.WriteHeader(&hdr); err != nil {
			return fmt.Errorf("image.layer: %q: %w", hdr.Name, err)
		}
		return nil
	}

	info, err := os.Lstat(e.source)
	if err != nil {
		return fmt.Errorf("image.layer: source %q: %w", e.source, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("image.layer: source %q: not a regular file (%s)", e.source, info.Mode().Type())
	}
	f, err := os.Open(e.source)
	if err != nil {
		return fmt.Errorf("image.layer: source %q: %w", e.source, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return fmt.Errorf("image.layer: source %q: %w", e.source, err)
	}
	if !os.SameFile(info, opened) {
		return fmt.Errorf("image.layer: source %q: replaced while being read", e.source)
	}

	hdr.Size = opened.Size()
	if err := tw.WriteHeader(&hdr); err != nil {
		return fmt.Errorf("image.layer: %q: %w", hdr.Name, err)
	}
	if _, err := io.CopyN(tw, &ctxReader{ctx: ctx, r: f}, hdr.Size); err != nil {
		return fmt.Errorf("image.layer: source %q: %w", e.source, err)
	}
	return nil
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// limitWriter fails the write that would take the stream past limit, so an
// oversize layer stops at the cap instead of after the last file.
type limitWriter struct {
	w     io.Writer
	limit int64
	n     int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n+int64(len(p)) > l.limit {
		return 0, fmt.Errorf("image.layer: uncompressed size exceeds %d bytes", l.limit)
	}
	n, err := l.w.Write(p)
	l.n += int64(n)
	return n, err
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// Layer is one OCI image layer: a gzipped tar filesystem diff.
// Manifest.layers carries Digest; config.rootfs.diff_ids carries DiffID.
// This is the OCI term, not a Dockerfile instruction and not a build stage.
type Layer struct {
	Digest    Digest // compressed blob (Descriptor.Digest)
	DiffID    DiffID // uncompressed tar (config.rootfs.diff_ids)
	Size      int64
	MediaType string
}

// IsZero reports an absent layer (config-only assemble, or unset Layout.Layer).
func (l Layer) IsZero() bool { return l.Digest.IsZero() }

// NewLayer writes one OCI layer to w from file and symlink placements and
// returns its digests. At most one Layer is what AssembleLayout adds over a base.
func NewLayer(ctx context.Context, w io.Writer, files []File, symlinks []Symlink) (Layer, error) {
	if w == nil {
		return Layer{}, fmt.Errorf("image.layer: nil writer")
	}
	blob, err := writeLayer(ctx, w, Spec{Files: files, Symlinks: symlinks}, maxLayerBytes)
	if err != nil {
		return Layer{}, err
	}
	return layerFromBlob(blob), nil
}

func layerFromBlob(b layerBlob) Layer {
	mt := b.descriptor.MediaType
	if mt == "" {
		mt = ocispec.MediaTypeImageLayerGzip
	}
	return Layer{
		Digest:    MustDigest(b.descriptor.Digest),
		DiffID:    MustDiffID(b.diffID),
		Size:      b.descriptor.Size,
		MediaType: mt,
	}
}
