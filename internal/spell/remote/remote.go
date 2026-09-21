// Package remote resolves a spell published as an OCI artifact and pinned by its
// manifest digest:
//
//	import "oci://ghcr.io/<owner>/<repo>/spells/<name>@sha256:<digest>" as name;
//
// It is the one resolver every spell consumer reaches: a magusfile import, the handle
// magus\harness.provider receives, and a remote cache backend selector. The artifact
// is one uncompressed tar layer of the spell directory, written by Pack, so publishing
// the same files twice yields the same digest.
package remote

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/oci"
	"github.com/egladman/magus/types"
)

// Scheme prefixes a remote spell import.
const Scheme = "oci://"

const (
	// artifactType is what a spell artifact's manifest says it is. A pull refuses any
	// other, so a digest that names someone else's artifact fails before a layer is read.
	artifactType   = "application/vnd.magus.spell.v1"
	layerMediaType = "application/vnd.magus.spell.layer.v1.tar"
	layerTitle     = "spell.tar"
	entryFile      = "spell.buzz"
)

// maxSpell bounds a spell's packed size. A spell is Buzz source; megabytes of it is
// not a spell.
const maxSpell = 8 << 20

const fetchTimeout = 2 * time.Minute

// Ref is a parsed remote spell import.
type Ref struct {
	Import string        // the import path as written
	OCI    oci.Reference // Digest is always set
}

// IsRef reports whether importPath is a remote spell import. It does not validate:
// Parse does, so a malformed reference reaches a coded error instead of the file
// search.
func IsRef(importPath string) bool {
	return strings.HasPrefix(importPath, Scheme)
}

// Parse validates a remote spell import. A reference with no manifest digest is
// MGS1041: a tag can move, so it pins nothing.
func Parse(importPath string) (Ref, error) {
	rest := strings.TrimPrefix(importPath, Scheme)
	if !strings.Contains(rest, "@") {
		return Ref{}, types.DiagnosticErrorf(types.RemoteSpellUnpinned,
			"remote spell %q names no manifest digest: a tag can move, so pin it with @sha256:<digest>", importPath)
	}
	ref, err := oci.ParseReference(rest)
	if err != nil {
		return Ref{}, fmt.Errorf("remote spell %q: %w", importPath, err)
	}
	return Ref{Import: importPath, OCI: ref}, nil
}

// Options configures Resolve. The zero value uses the user cache and
// http.DefaultClient.
type Options struct {
	CacheRoot string
	Client    *http.Client
}

// EntryPath parses importPath, resolves it into the user cache, and returns the
// local path of its spell.buzz. It is the call every consumer makes.
func EntryPath(ctx context.Context, importPath string) (string, error) {
	ref, err := Parse(importPath)
	if err != nil {
		return "", err
	}
	dir, err := Resolve(ctx, ref, Options{})
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, entryFile), nil
}

// Resolve returns a local directory holding ref's verified spell, pulling it only
// when no cached copy verifies. The cache is keyed by manifest digest, and every call
// re-verifies the cached manifest, layer and extracted files before trusting them, so
// a hand-edited cache is replaced rather than run. A registry that serves bytes other
// than the pinned ones is MGS1042. With MAGUS_OFFLINE set, only the cache is read.
// Safe for concurrent use: a pull lands in a temporary directory renamed into place,
// and a verified entry is never removed.
func Resolve(ctx context.Context, ref Ref, opts Options) (string, error) {
	root := opts.CacheRoot
	if root == "" {
		base, err := config.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("remote spell %s: locate cache dir: %w", ref.Import, err)
		}
		root = filepath.Join(base, "magus", "spells")
	}
	slot := filepath.Join(root, ref.OCI.Digest.Algorithm().String()+"-"+ref.OCI.Digest.Encoded())
	src := filepath.Join(slot, "src")
	verr := verifySlot(slot, ref.OCI.Digest)
	if verr == nil {
		return src, nil
	}
	_, statErr := os.Stat(slot)
	stale := statErr == nil
	if offline() {
		if stale {
			return "", fmt.Errorf("remote spell %s: cached copy does not verify and MAGUS_OFFLINE is set: %w", ref.Import, verr)
		}
		return "", fmt.Errorf("remote spell %s is not cached and MAGUS_OFFLINE is set", ref.Import)
	}
	if stale {
		slog.WarnContext(ctx, "remote spell cache entry does not verify; pulling it again", "dir", slot, "err", verr)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("remote spell %s: %w", ref.Import, err)
	}
	tmp, err := os.MkdirTemp(root, ".pull-")
	if err != nil {
		return "", fmt.Errorf("remote spell %s: %w", ref.Import, err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := pull(ctx, opts.Client, ref, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, slot); err == nil {
		return src, nil
	}
	// The slot is occupied: either a concurrent resolve won the rename, or it holds
	// the entry that failed verification above. Only the second is moved aside, so a
	// path another resolve already returned stays valid.
	if verifySlot(slot, ref.OCI.Digest) == nil {
		return src, nil
	}
	aside := tmp + ".stale"
	if err := os.Rename(slot, aside); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("remote spell %s: move unverified cache entry aside: %w", ref.Import, err)
	}
	defer func() { _ = os.RemoveAll(aside) }()
	if err := os.Rename(tmp, slot); err != nil {
		if verifySlot(slot, ref.OCI.Digest) == nil {
			return src, nil
		}
		return "", fmt.Errorf("remote spell %s: %w", ref.Import, err)
	}
	return src, nil
}

// pull fetches ref into dir as manifest.json, spell.tar and the extracted src/.
func pull(ctx context.Context, client *http.Client, ref Ref, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	c := &oci.Client{HTTP: client}
	art, err := c.Artifact(ctx, ref.OCI, artifactType)
	if err != nil {
		return pullError(ref, err)
	}
	layer, err := art.Layer(ctx, layerTitle)
	if err != nil {
		return pullError(ref, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), art.Raw, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, layerTitle), layer, 0o644); err != nil {
		return err
	}
	if err := extract(layer, filepath.Join(dir, "src")); err != nil {
		return fmt.Errorf("remote spell %s: %w", ref.Import, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", entryFile)); err != nil {
		return fmt.Errorf("remote spell %s holds no %s", ref.Import, entryFile)
	}
	return nil
}

// pullError codes a digest failure as MGS1042, keeping the oci sentinel matchable.
func pullError(ref Ref, err error) error {
	if errors.Is(err, oci.ErrManifestDigest) || errors.Is(err, oci.ErrBlobDigest) {
		return types.WrapDiagnostic(types.RemoteSpellDigestMismatch, err,
			"remote spell %s: the registry served bytes that do not match the pin: %v", ref.Import, err)
	}
	return fmt.Errorf("remote spell %s: %w", ref.Import, err)
}

// verifySlot checks a cache entry end to end: the manifest hashes to pin, the layer
// hashes to the manifest's descriptor, and src/ holds exactly the layer's files.
func verifySlot(slot string, pin digest.Digest) error {
	raw, err := os.ReadFile(filepath.Join(slot, "manifest.json"))
	if err != nil {
		return err
	}
	if got := pin.Algorithm().FromBytes(raw); got != pin {
		return fmt.Errorf("manifest hashes to %s", got)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	// First match, as oci.Artifact.Layer picks when pulling.
	i := slices.IndexFunc(m.Layers, func(d ocispec.Descriptor) bool {
		return d.Annotations[ocispec.AnnotationTitle] == layerTitle
	})
	if i < 0 {
		return fmt.Errorf("manifest names no %s layer", layerTitle)
	}
	desc := m.Layers[i]
	if err := desc.Digest.Validate(); err != nil {
		return fmt.Errorf("manifest names no usable %s layer: %w", layerTitle, err)
	}
	layer, err := os.ReadFile(filepath.Join(slot, layerTitle))
	if err != nil {
		return err
	}
	if desc.Digest.Algorithm().FromBytes(layer) != desc.Digest {
		return fmt.Errorf("%s does not match its descriptor", layerTitle)
	}
	want, err := tarFiles(layer)
	if err != nil {
		return err
	}
	have, err := dirFiles(filepath.Join(slot, "src"))
	if err != nil {
		return err
	}
	if !slices.Equal(want, have) {
		return errors.New("src/ does not match the layer")
	}
	return nil
}

// fileSum is one file's slash path and sha256, the unit extract and the cache check
// agree on.
type fileSum struct {
	path string
	sum  [sha256.Size]byte
}

func sortSums(s []fileSum) []fileSum {
	slices.SortFunc(s, func(a, b fileSum) int { return strings.Compare(a.path, b.path) })
	return s
}

// walkDir calls fn with the slash path and contents of each regular file under dir.
// Anything else is an error: a spell holds source files, and a symlink could point
// anywhere.
func walkDir(dir string, fn func(name string, body []byte) error) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), body)
	})
}

func dirFiles(dir string) ([]fileSum, error) {
	var out []fileSum
	err := walkDir(dir, func(name string, body []byte) error {
		out = append(out, fileSum{path: name, sum: sha256.Sum256(body)})
		return nil
	})
	return sortSums(out), err
}

func tarFiles(layer []byte) ([]fileSum, error) {
	var out []fileSum
	err := walkTar(layer, func(name string, body []byte) error {
		out = append(out, fileSum{path: name, sum: sha256.Sum256(body)})
		return nil
	})
	return sortSums(out), err
}

// walkTar calls fn with each regular file in a spell layer. It refuses any other
// entry type, a path that escapes the directory, a path in any form but its clean
// one, and a path seen twice: each would let the layer and its extraction disagree.
func walkTar(layer []byte, fn func(name string, body []byte) error) error {
	tr := tar.NewReader(bytes.NewReader(layer))
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read spell layer: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return fmt.Errorf("spell layer entry %q is not a regular file", hdr.Name)
		}
		name := hdr.Name
		if !filepath.IsLocal(filepath.FromSlash(name)) || path.Clean(name) != name || strings.Contains(name, `\`) {
			return fmt.Errorf("spell layer entry %q is not a clean path inside the spell directory", hdr.Name)
		}
		if seen[name] {
			return fmt.Errorf("spell layer entry %q appears twice", hdr.Name)
		}
		seen[name] = true
		body, err := io.ReadAll(io.LimitReader(tr, maxSpell+1))
		if err != nil {
			return fmt.Errorf("read spell layer entry %q: %w", hdr.Name, err)
		}
		if len(body) > maxSpell {
			return fmt.Errorf("spell layer entry %q exceeds %d bytes", hdr.Name, maxSpell)
		}
		if err := fn(name, body); err != nil {
			return err
		}
	}
}

func extract(layer []byte, dst string) error {
	return walkTar(layer, func(name string, body []byte) error {
		target := filepath.Join(dst, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
}

// Pack writes dir as a spell layer: its regular files sorted by path, each with fixed
// mode, owner and time, so the same files always pack to the same bytes and the
// published manifest digest is reproducible. dir must hold spell.buzz.
func Pack(dir string) ([]byte, error) {
	type file struct {
		name string
		body []byte
	}
	var files []file
	if err := walkDir(dir, func(name string, body []byte) error {
		files = append(files, file{name, body})
		return nil
	}); err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(files, func(f file) bool { return f.name == entryFile }) {
		return nil, fmt.Errorf("%s holds no %s", dir, entryFile)
	}
	slices.SortFunc(files, func(a, b file) int { return strings.Compare(a.name, b.name) })
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     f.name,
			Mode:     0o644,
			Size:     int64(len(f.body)),
			ModTime:  time.Unix(0, 0),
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("pack %s: %w", f.name, err)
		}
		if _, err := tw.Write(f.body); err != nil {
			return nil, fmt.Errorf("pack %s: %w", f.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("pack %s: %w", dir, err)
	}
	if buf.Len() > maxSpell {
		return nil, fmt.Errorf("%s packs to %d bytes, over the %d byte cap", dir, buf.Len(), maxSpell)
	}
	return buf.Bytes(), nil
}

// Publish packs dir and pushes it to dest, returning the manifest digest a pinned
// import names.
func Publish(ctx context.Context, c *oci.Client, dest oci.Reference, dir string) (digest.Digest, error) {
	layer, err := Pack(dir)
	if err != nil {
		return "", err
	}
	return c.Push(ctx, dest, artifactType, oci.Layer{Name: layerTitle, MediaType: layerMediaType, Payload: layer})
}

// offline matches internal/registry: MAGUS_OFFLINE set to anything but 0 or false.
func offline() bool {
	v := os.Getenv("MAGUS_OFFLINE")
	return v != "" && v != "0" && v != "false"
}
