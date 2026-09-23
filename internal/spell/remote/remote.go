// Package remote resolves a spell published as an OCI artifact, imported by its
// repository path the way a Go import path is its repository:
//
//	import "ghcr.io/<owner>/<repo>/spells/<name>";
//
// magus.yaml declares the tag each path tracks, magus.lock pins the manifest digest
// that tag resolved to, and only Relock, which the update charm drives, ever asks a
// registry what a tag means. Every other load reads the locked digest through
// LoadImports, the one resolver every spell consumer reaches: a magusfile import, the
// handle magus\harness.provider receives, and a remote cache backend selector. The
// artifact is one uncompressed tar layer of the spell directory's tracked files,
// written by Pack, so publishing the same commit twice yields the same digest.
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
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/oci"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

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

// Ref is a remote spell pinned to one manifest.
type Ref struct {
	Import string        // the import path, e.g. ghcr.io/team/spells/lint
	OCI    oci.Reference // Digest is always set
}

// Pinned is the Ref for importPath at the manifest digest pin.
func Pinned(importPath string, pin digest.Digest) (Ref, error) {
	if !spells.IsRemoteImport(importPath) {
		return Ref{}, fmt.Errorf("remote spell %q: not a registry path", importPath)
	}
	if err := pin.Validate(); err != nil {
		return Ref{}, fmt.Errorf("remote spell %s: %w", importPath, err)
	}
	ref, err := oci.ParseRepository(importPath)
	if err != nil {
		return Ref{}, fmt.Errorf("remote spell %s: %w", importPath, err)
	}
	ref.Digest = pin
	return Ref{Import: importPath, OCI: ref}, nil
}

// Options configures Resolve. The zero value uses the user cache and
// http.DefaultClient, and pulls anonymously.
type Options struct {
	CacheRoot string
	Client    *http.Client
	// Username and Password authenticate a pull from a private repository; see
	// oci.Client.
	Username string
	Password string
	// Connect, when set, supplies the registry client in place of Client, Username and
	// Password. It is called only when a pull is needed, so a verified cache entry
	// resolves no secret.
	Connect func(ctx context.Context) (*oci.Client, error)
}

// cacheRoot is where verified spells live: opts.CacheRoot, or the user cache.
func (o Options) cacheRoot() (string, error) {
	if o.CacheRoot != "" {
		return o.CacheRoot, nil
	}
	base, err := config.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("remote spell: locate cache dir: %w", err)
	}
	return filepath.Join(base, "magus", "spells"), nil
}

// verified holds the cache slots this process has already verified, keyed by slot
// path, which is the digest under its cache root. The pre-load check and the import
// that binds the spell then verify once between them.
var verified sync.Map

// Resolve returns a local directory holding ref's verified spell, pulling it only
// when no cached copy verifies. The cache is keyed by manifest digest, and each
// process re-verifies a cached manifest, layer and extracted files once before
// trusting them, so a hand-edited cache is replaced rather than run. A registry that
// serves bytes other than the pinned ones is MGS1042. With MAGUS_OFFLINE set, only
// the cache is read, and a cached copy that does not verify is MGS1042 too.
// Safe for concurrent use: a pull lands in a temporary directory renamed into place,
// and a verified entry is never removed.
func Resolve(ctx context.Context, ref Ref, opts Options) (string, error) {
	root, err := opts.cacheRoot()
	if err != nil {
		return "", err
	}
	slot := filepath.Join(root, ref.OCI.Digest.Algorithm().String()+"-"+ref.OCI.Digest.Encoded())
	src := filepath.Join(slot, "src")
	if _, ok := verified.Load(slot); ok {
		return src, nil
	}
	if src, err = resolveSlot(ctx, ref, opts, root, slot); err != nil {
		return "", err
	}
	verified.Store(slot, struct{}{})
	return src, nil
}

func resolveSlot(ctx context.Context, ref Ref, opts Options, root, slot string) (string, error) {
	src := filepath.Join(slot, "src")
	verr := verifySlot(slot, ref.OCI.Digest)
	if verr == nil {
		return src, nil
	}
	_, statErr := os.Stat(slot)
	stale := statErr == nil
	if offline() {
		if stale {
			return "", types.WrapDiagnostic(types.RemoteSpellDigestMismatch, verr,
				"remote spell %s: the cached copy does not match the pin and MAGUS_OFFLINE forbids a fresh pull: %v", ref.Import, verr)
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
	c, err := opts.client(ctx)
	if err != nil {
		return "", fmt.Errorf("remote spell %s: %w", ref.Import, err)
	}
	if err := pull(ctx, c, ref, tmp); err != nil {
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
func pull(ctx context.Context, c *oci.Client, ref Ref, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
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

// dirFiles lists every file under dir. Anything but a regular file is an error: the
// cache holds only what extract wrote.
func dirFiles(dir string) ([]fileSum, error) {
	names, err := dirEntries(dir)
	if err != nil {
		return nil, err
	}
	out := make([]fileSum, 0, len(names))
	for _, name := range names {
		body, err := readRegular(dir, name)
		if err != nil {
			return nil, err
		}
		out = append(out, fileSum{path: name, sum: sha256.Sum256(body)})
	}
	return sortSums(out), nil
}

// dirEntries lists the slash paths of every non-directory entry under dir.
func dirEntries(dir string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	return names, err
}

// readRegular reads dir/name, refusing anything but a regular file: a spell holds
// source files, and a symlink could point anywhere.
func readRegular(dir, name string) ([]byte, error) {
	p := filepath.Join(dir, filepath.FromSlash(name))
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	return os.ReadFile(p)
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

// Pack writes dir as a spell layer: the files under it that vcs tracks, sorted by
// path, each with fixed mode, owner and time. An untracked or ignored file never
// reaches the layer, so the digest is a function of what the repository holds and
// is the same on every checkout. dir must hold a tracked spell.buzz, and a tracked
// entry that is not a regular file is an error.
func Pack(ctx context.Context, dir string, vcs types.TrackedFileReporter) ([]byte, error) {
	candidates, err := dirEntries(dir)
	if err != nil {
		return nil, err
	}
	var tracked []string
	if len(candidates) > 0 {
		reported, err := vcs.TrackedFiles(ctx, dir, candidates)
		if err != nil {
			return nil, fmt.Errorf("pack %s: list tracked files: %w", dir, err)
		}
		// A backend may read a path as a pattern; keep only the entries asked about.
		tracked = slices.DeleteFunc(slices.Clone(reported), func(p string) bool {
			return !slices.Contains(candidates, p)
		})
	}
	slices.Sort(tracked)
	tracked = slices.Compact(tracked)
	if !slices.Contains(tracked, entryFile) {
		return nil, fmt.Errorf("%s holds no tracked %s", dir, entryFile)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, name := range tracked {
		body, err := readRegular(dir, name)
		if err != nil {
			return nil, fmt.Errorf("pack: %w", err)
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			ModTime:  time.Unix(0, 0),
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("pack %s: %w", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			return nil, fmt.Errorf("pack %s: %w", name, err)
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

// Provenance is what a spell artifact records about its origin, as the OCI standard
// manifest annotations. A zero field is omitted rather than written empty.
type Provenance struct {
	Title    string    // org.opencontainers.image.title
	Source   string    // org.opencontainers.image.source
	Revision string    // org.opencontainers.image.revision
	Created  time.Time // org.opencontainers.image.created, written as RFC 3339 UTC
}

// Annotations renders p as manifest annotations.
func (p Provenance) Annotations() map[string]string {
	out := map[string]string{}
	for k, v := range map[string]string{
		ocispec.AnnotationTitle:    p.Title,
		ocispec.AnnotationSource:   p.Source,
		ocispec.AnnotationRevision: p.Revision,
	} {
		if v != "" {
			out[k] = v
		}
	}
	if !p.Created.IsZero() {
		out[ocispec.AnnotationCreated] = p.Created.UTC().Format(time.RFC3339)
	}
	return out
}

// commitFinder is the one VCS call ReadProvenance needs.
type commitFinder interface {
	FindCommit(ctx context.Context, dir, rev string) (types.Commit, error)
}

// ReadProvenance describes the checked-out revision holding dir. Created is that
// revision's COMMIT time, never the wall clock, so building one commit twice produces
// one digest; SOURCE_DATE_EPOCH, when set, overrides it the way reproducible-builds
// tooling expects. Source is the default remote as a browsable https URL with any
// userinfo dropped, empty when the backend reports none. Title is dir's base name.
func ReadProvenance(ctx context.Context, vcs commitFinder, dir string) (Provenance, error) {
	c, err := vcs.FindCommit(ctx, dir, "")
	if err != nil {
		return Provenance{}, fmt.Errorf("read revision of %s: %w", dir, err)
	}
	p := Provenance{Title: filepath.Base(dir), Revision: c.ID, Created: c.Date}
	if raw, ok := os.LookupEnv("SOURCE_DATE_EPOCH"); ok {
		secs, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return Provenance{}, fmt.Errorf("SOURCE_DATE_EPOCH %q is not a count of seconds: %w", raw, err)
		}
		p.Created = time.Unix(secs, 0)
	}
	if r, ok := vcs.(types.RemoteReporter); ok {
		if remote, err := r.RemoteURL(ctx, dir); err == nil {
			p.Source = SourceURL(remote)
		}
	}
	return p, nil
}

// SourceURL turns a VCS remote into the https URL org.opencontainers.image.source
// expects: scp-style ssh ("git@host:owner/repo.git") and ssh:// become https, userinfo
// is dropped so a token embedded in a clone URL never reaches a public manifest, and a
// trailing .git is trimmed. A remote it cannot read returns "".
func SourceURL(remote string) string {
	remote = strings.TrimSpace(remote)
	if !strings.Contains(remote, "://") {
		// scp-like syntax: [user@]host:path, with no scheme.
		hostPart, path, ok := strings.Cut(remote, ":")
		if !ok || strings.Contains(hostPart, "/") {
			return ""
		}
		if _, host, ok := strings.Cut(hostPart, "@"); ok {
			hostPart = host
		}
		remote = "https://" + hostPart + "/" + strings.TrimPrefix(path, "/")
	}
	u, err := url.Parse(remote)
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Host // userinfo is u.User, so it is already gone
	switch u.Scheme {
	case "https", "http":
	case "ssh", "git":
		// An ssh or git port says nothing about where the web UI listens.
		host = u.Hostname()
	default:
		return ""
	}
	return (&url.URL{Scheme: "https", Host: host, Path: strings.TrimSuffix(u.Path, ".git")}).String()
}

// Build packs dir into the artifact a push uploads, with prov as its manifest
// annotations. The digest of its Manifest is the one a push of the same commit prints,
// on any machine, so CI can compare a local build against a published pin.
func Build(ctx context.Context, dir string, tracked types.TrackedFileReporter, prov Provenance) (oci.Content, error) {
	layer, err := Pack(ctx, dir, tracked)
	if err != nil {
		return oci.Content{}, err
	}
	return oci.Content{
		ArtifactType: artifactType,
		Annotations:  prov.Annotations(),
		Layers:       []oci.Layer{{Name: layerTitle, MediaType: layerMediaType, Payload: layer}},
	}, nil
}

// Pin resolves ref to the manifest digest it names now, checking the artifact type. A
// ref that already carries a digest is verified against it and returned as is.
func Pin(ctx context.Context, c *oci.Client, ref oci.Reference) (oci.Reference, error) {
	art, err := c.Artifact(ctx, ref, artifactType)
	if err != nil {
		return oci.Reference{}, err
	}
	pinned := ref
	if pinned.Digest == "" {
		pinned.Digest = digest.FromBytes(art.Raw)
	}
	return pinned, nil
}

// Unpack copies the spell Resolve cached at src into dst, which must not exist or be
// empty. It re-extracts the verified layer rather than copying src, so dst holds
// exactly what the artifact carries.
func Unpack(src, dst string) error {
	if entries, err := os.ReadDir(dst); err == nil && len(entries) > 0 {
		return fmt.Errorf("unpack into %s: directory is not empty", dst)
	}
	layer, err := os.ReadFile(filepath.Join(filepath.Dir(src), layerTitle))
	if err != nil {
		return fmt.Errorf("unpack into %s: %w", dst, err)
	}
	if err := extract(layer, dst); err != nil {
		return fmt.Errorf("unpack into %s: %w", dst, err)
	}
	return nil
}

func (o Options) client(ctx context.Context) (*oci.Client, error) {
	if o.Connect != nil {
		return o.Connect(ctx)
	}
	return &oci.Client{HTTP: o.Client, Username: o.Username, Password: o.Password}, nil
}

// offline matches internal/registry: MAGUS_OFFLINE set to anything but 0 or false.
func offline() bool {
	v := os.Getenv("MAGUS_OFFLINE")
	return v != "" && v != "0" && v != "false"
}
