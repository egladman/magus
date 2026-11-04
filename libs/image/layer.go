package image

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// maxLayerBytes caps the uncompressed tar, matching the archive module's default.
const maxLayerBytes int64 = 10 << 30

// entry is one tar header of the new layer.
type entry struct {
	path     string
	kind     EntryKind
	source   string // EntryFile: the host file
	size     int64  // EntryFile: the host file's size when listed
	target   string // EntrySymlink
	mode     Mode
	uid, gid int
	origin   string
}

// EntryKind is the kind of one tar entry.
type EntryKind string

const (
	EntryFile      EntryKind = "file"
	EntrySymlink   EntryKind = "symlink"
	EntryDirectory EntryKind = "directory"
)

// Entry is one tar entry of a new layer, as its header is written. Origin
// names the Spec item that placed it, such as "files[0]" or "symlinks[0]". A
// tree's names the file relative to its source: "trees[1] etc/ssl/ca.pem". A
// directory scratch adds above a placement is "parent".
type Entry struct {
	Path     string // absolute; the tar name drops the leading slash
	Kind     EntryKind
	Mode     Mode // a symlink's is 0777, as tar writes it
	UID, GID int
	Size     int64     // 0 except for a file
	Target   string    // a symlink's
	ModTime  time.Time // always the Unix epoch
	Origin   string
}

// Layer is the receipt of the one layer an assemble wrote. DiffID is the sha256
// of the uncompressed tar, which config.rootfs.diff_ids lists. Entries are in
// tar order, and the counts say how many of each kind it holds.
type Layer struct {
	ocispec.Descriptor
	DiffID      digest.Digest
	Entries     []Entry
	Files       int
	Symlinks    int
	Directories int
}

// Entries lists every entry the new layer of spec holds, in tar order, from
// the same list Assemble writes. It reads the spec's host files and never the
// base, and writes nothing. An empty list means spec adds no layer.
func Entries(ctx context.Context, spec Spec) ([]Entry, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	entries, err := layerEntries(ctx, spec)
	if err != nil {
		return nil, err
	}
	return listEntries(entries), nil
}

func listEntries(entries []entry) []Entry {
	out := make([]Entry, len(entries))
	for i, e := range entries {
		h := e.header()
		out[i] = Entry{
			Path: e.path, Kind: e.kind, Mode: Mode(h.Mode), UID: h.Uid, GID: h.Gid,
			Size: h.Size, Target: h.Linkname, ModTime: h.ModTime, Origin: e.origin,
		}
	}
	return out
}

// layerEntries lists the spec's entries in path order, adding unplaced parents as
// root 0755 directories on scratch only: over a base an upper directory entry replaces
// the lower one's mode and owner (it would reset /tmp's 1777), and an unpacker creates
// a missing parent itself.
func layerEntries(ctx context.Context, spec Spec) ([]entry, error) {
	var out []entry
	for i, f := range spec.Files {
		size, err := regularSize(f.Source)
		if err != nil {
			return nil, fmt.Errorf("image.assemble: files[%d] %s: %w", i, f.Path, err)
		}
		out = append(out, entry{path: f.Path, kind: EntryFile, source: f.Source, size: size, mode: f.Mode, uid: f.UID, gid: f.GID, origin: fmt.Sprintf("files[%d]", i)})
	}
	for i, l := range spec.Symlinks {
		out = append(out, entry{path: l.Path, kind: EntrySymlink, target: l.Target, uid: l.UID, gid: l.GID, origin: fmt.Sprintf("symlinks[%d]", i)})
	}
	for i, d := range spec.Directories {
		out = append(out, entry{path: d.Path, kind: EntryDirectory, mode: d.Mode, uid: d.UID, gid: d.GID, origin: fmt.Sprintf("directories[%d]", i)})
	}
	for i, t := range spec.Trees {
		more, err := t.expand(ctx, fmt.Sprintf("trees[%d]", i))
		if err != nil {
			return nil, fmt.Errorf("image.assemble: trees[%d] %s: %w", i, t.Source, err)
		}
		out = append(out, more...)
	}
	if err := checkPlacements(out); err != nil {
		return nil, err
	}
	if spec.Scratch {
		out = withParents(out)
	}
	slices.SortFunc(out, func(a, b entry) int { return strings.Compare(a.path, b.path) })
	return out, nil
}

func withParents(entries []entry) []entry {
	have := make(map[string]bool, len(entries))
	for _, e := range entries {
		have[e.path] = true
	}
	n := len(entries)
	for _, e := range entries[:n] {
		for dir := path.Dir(e.path); dir != "/" && !have[dir]; dir = path.Dir(dir) {
			have[dir] = true
			entries = append(entries, entry{path: dir, kind: EntryDirectory, mode: ModeDirectory, origin: "parent"})
		}
	}
	return entries
}

func regularSize(source string) (int64, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("source %s: not a regular file (%s)", source, info.Mode().Type())
	}
	return info.Size(), nil
}

func (e entry) header() tar.Header {
	h := tar.Header{
		Name:    strings.TrimPrefix(e.path, "/"),
		Mode:    int64(e.mode),
		Uid:     e.uid,
		Gid:     e.gid,
		ModTime: time.Unix(0, 0),
	}
	switch e.kind {
	case EntryFile:
		h.Typeflag, h.Size = tar.TypeReg, e.size
	case EntrySymlink:
		h.Typeflag, h.Linkname, h.Mode = tar.TypeSymlink, e.target, 0o777
	case EntryDirectory:
		h.Typeflag = tar.TypeDir
		h.Name += "/"
	}
	return h
}

type layerBlob struct {
	diffID     digest.Digest
	descriptor ocispec.Descriptor
}

// writeLayer writes entries to w as one deterministic tar+gzip and returns its DiffID
// (sha256 of the tar) and descriptor (sha256 and length of the bytes written to w).
// Each byte is hashed once per digest on the way through. limit bounds the tar.
func writeLayer(ctx context.Context, w io.Writer, entries []entry, limit int64) (layerBlob, error) {
	blob := digest.Canonical.Digester()
	size := &countWriter{}
	gz, err := gzip.NewWriterLevel(io.MultiWriter(w, blob.Hash(), size), gzip.DefaultCompression)
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
		diffID:     diff.Digest(),
		descriptor: ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: blob.Digest(), Size: size.n},
	}, nil
}

func writeEntry(ctx context.Context, tw *tar.Writer, e entry) error {
	hdr := e.header()
	if e.kind != EntryFile {
		if err := tw.WriteHeader(&hdr); err != nil {
			return fmt.Errorf("image.layer: %s: %w", e.path, err)
		}
		return nil
	}
	info, err := os.Lstat(e.source)
	if err != nil {
		return fmt.Errorf("image.layer: source %s: %w", e.source, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("image.layer: source %s: not a regular file (%s)", e.source, info.Mode().Type())
	}
	f, err := os.Open(e.source)
	if err != nil {
		return fmt.Errorf("image.layer: source %s: %w", e.source, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return fmt.Errorf("image.layer: source %s: %w", e.source, err)
	}
	if !os.SameFile(info, opened) {
		return fmt.Errorf("image.layer: source %s: replaced while being read", e.source)
	}
	if opened.Size() != e.size {
		return fmt.Errorf("image.layer: source %s: %d bytes when listed, %d now: it changed during the assemble", e.source, e.size, opened.Size())
	}
	if err := tw.WriteHeader(&hdr); err != nil {
		return fmt.Errorf("image.layer: %s: %w", e.path, err)
	}
	if _, err := io.CopyN(tw, &ctxReader{ctx: ctx, r: f}, hdr.Size); err != nil {
		return fmt.Errorf("image.layer: source %s: %w", e.source, err)
	}
	return nil
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// limitWriter fails the write that would take the stream past limit, so an oversize
// layer stops at the cap instead of after the last file.
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
