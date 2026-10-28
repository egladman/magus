package image

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/cache/reflink"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/oci"
)

const (
	layoutMarker  = `{"imageLayoutVersion":"1.0.0"}`
	layoutVersion = "1.0.0"
	// transferConcurrency bounds parallel blob transfers to one registry: the limit is the
	// registry and the link, not the CPU count.
	transferConcurrency = 8
)

// staging is a layout being written beside its destination. Nothing is visible at dest
// until commit, and discard removes every trace, so a failed write leaves dest as it was.
// Blobs land in their final place under blobs/sha256 as they are written or fetched,
// each hashed once on the way in.
type staging struct {
	dest, dir, blobs string

	mu   sync.Mutex
	have map[digest.Digest]bool
}

func newStaging(dest string) (*staging, error) {
	dest = filepath.Clean(dest)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(filepath.Dir(dest), fmt.Sprintf(".%s.staging-*", filepath.Base(dest)))
	if err != nil {
		return nil, fmt.Errorf("staging directory: %w", err)
	}
	s := &staging{dest: dest, dir: dir, blobs: filepath.Join(dir, "blobs", "sha256"), have: map[digest.Digest]bool{}}
	// MkdirTemp makes 0700 and MkdirAll honors the umask, so chmod spells the mode.
	for _, d := range []string{dir, filepath.Join(dir, "blobs"), s.blobs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			s.discard()
			return nil, err
		}
		if err := os.Chmod(d, 0o755); err != nil {
			s.discard()
			return nil, err
		}
	}
	return s, nil
}

func (s *staging) discard() { _ = os.RemoveAll(s.dir) }

func (s *staging) path(d digest.Digest) string { return filepath.Join(s.blobs, d.Encoded()) }

// claim reports whether d still needs writing and marks it taken.
func (s *staging) claim(d digest.Digest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.have[d] {
		return false
	}
	s.have[d] = true
	return true
}

// put writes data as a blob and describes it.
func (s *staging) put(mediaType string, data []byte) (ocispec.Descriptor, error) {
	desc := ocispec.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(data), Size: int64(len(data))}
	if !s.claim(desc.Digest) {
		return desc, nil
	}
	if err := file.WriteFileAtomic(s.path(desc.Digest), data, 0o644); err != nil {
		return ocispec.Descriptor{}, err
	}
	return desc, nil
}

// putJSON marshals v and puts it.
func (s *staging) putJSON(mediaType string, v any) (ocispec.Descriptor, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return s.put(mediaType, data)
}

// write streams a blob whose digest is known only at the end: fill writes it through
// a buffer, then the file is renamed to the digest fill reports.
func (s *staging) write(fill func(io.Writer) (ocispec.Descriptor, error)) (ocispec.Descriptor, error) {
	f, err := os.CreateTemp(s.blobs, ".blob-*")
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	// gzip writes in small chunks; the buffer turns them into large file writes.
	// BenchmarkAssembleLayer measures this path (264 MB/s on an M5, one 16 MiB file).
	buf := bufio.NewWriterSize(f, 1<<20)
	desc, err := fill(buf)
	if err == nil {
		err = buf.Flush()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return ocispec.Descriptor{}, err
	}
	if !s.claim(desc.Digest) {
		return desc, nil
	}
	return desc, os.Rename(tmp, s.path(desc.Digest))
}

// link installs desc from another layout. The size is checked, not the hash: the blob
// was verified when it was pulled or written, and rehashing a base costs O(image) per
// assemble. It hardlinks only a 0644 source, so it never changes another target's
// output; otherwise it reflinks or copies.
func (s *staging) link(ctx context.Context, layout string, desc ocispec.Descriptor) error {
	if err := checkDigest(desc.Digest); err != nil {
		return fmt.Errorf("blob %q: %w", desc.Digest, err)
	}
	if !s.claim(desc.Digest) {
		return nil
	}
	src, dst := blobPath(layout, desc.Digest), s.path(desc.Digest)
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("blob %s: %w", desc.Digest, err)
	}
	if info.Size() != desc.Size {
		return fmt.Errorf("blob %s: size is %d bytes, descriptor says %d", desc.Digest, info.Size(), desc.Size)
	}
	if info.Mode().Perm() == 0o644 && os.Link(src, dst) == nil {
		return nil
	}
	if reflink.Clone(src, dst) != nil {
		if err := copyFile(ctx, src, dst); err != nil {
			return fmt.Errorf("blob %s: %w", desc.Digest, err)
		}
	}
	return os.Chmod(dst, 0o644)
}

func copyFile(ctx context.Context, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return file.WriteReader(dst, &ctxReader{ctx: ctx, r: in}, 0o644)
}

// fetch downloads desc from repo straight into its blob path, verified while it streams.
func (s *staging) fetch(ctx context.Context, client *oci.Client, repo oci.Reference, desc ocispec.Descriptor) error {
	if err := checkDigest(desc.Digest); err != nil {
		return fmt.Errorf("blob %q: %w", desc.Digest, err)
	}
	if !s.claim(desc.Digest) {
		return nil
	}
	f, err := os.CreateTemp(s.blobs, ".fetch-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	buf := bufio.NewWriterSize(f, 1<<20)
	err = client.GetBlob(ctx, repo, desc, buf)
	if err == nil {
		err = buf.Flush()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644)
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, s.path(desc.Digest))
}

// commit writes oci-layout and index.json naming manifests, then swaps the staging
// directory in for dest. The first manifest is the image; any after it are referrers.
func (s *staging) commit(manifests ...ocispec.Descriptor) error {
	for _, m := range manifests {
		if !s.has(m.Digest) {
			return fmt.Errorf("index.json names %s, which no blob holds", m.Digest)
		}
	}
	index, err := json.Marshal(ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, Manifests: manifests})
	if err != nil {
		return err
	}
	if err := file.WriteFileAtomic(filepath.Join(s.dir, "oci-layout"), []byte(layoutMarker), 0o644); err != nil {
		return err
	}
	if err := file.WriteFileAtomic(filepath.Join(s.dir, "index.json"), index, 0o644); err != nil {
		return err
	}
	if err := syncDir(s.blobs); err != nil {
		return err
	}
	return replaceDir(s.dir, s.dest)
}

func (s *staging) has(d digest.Digest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.have[d]
}

// replaceDir swaps staging in for dest: dest moves aside, staging takes its place, and
// the old tree is removed last. If installing fails the old tree goes back.
func replaceDir(staging, dest string) error {
	aside := staging + ".old"
	hadOld := true
	if err := os.Rename(dest, aside); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("move %s aside: %w", dest, err)
		}
		hadOld = false
	}
	if err := os.Rename(staging, dest); err != nil {
		if hadOld {
			_ = os.Rename(aside, dest)
		}
		return fmt.Errorf("install %s: %w", dest, err)
	}
	if hadOld {
		if err := os.RemoveAll(aside); err != nil {
			return fmt.Errorf("%s is installed but the old tree %s remains: %w", dest, aside, err)
		}
	}
	return nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func blobPath(layout string, d digest.Digest) string {
	return filepath.Join(layout, "blobs", d.Algorithm().String(), d.Encoded())
}
