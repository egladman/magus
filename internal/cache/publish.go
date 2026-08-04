package cache

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/secret"
)

// An OUTPUT BUNDLE is how one run's captured output travels to another machine on
// purpose, as opposed to riding along with a replayable cache entry.
//
// It exists because the two things a team wants to share are asymmetric. A PASSING
// run's output travels automatically inside the remote cache artifact, because that
// artifact is going to be pushed anyway. A FAILING run is never cached and never
// pushed - and a failure is the output humans most want to hand to a teammate. So
// publishing is explicit (`magus query output <ref> --publish`), and what it uploads
// is deliberately NOT a cache artifact:
//
//   - It carries no manifest and no cas blobs, so it can never be imported as a
//     replayable entry. A published failure cannot become someone's "successful"
//     cached build, whatever the backend is asked for.
//   - It is stored under its own key space (bundleProject), so it cannot collide
//     with, or be mistaken for, the artifact for the same cache key.
//   - It is signed with the same key and verified with the same trust set, so an
//     untrusted or tampered bundle is refused exactly as an artifact would be.
//
// The bytes still leave the machine, which is why nothing here is automatic: a
// failing run's output can contain anything the target printed.

// bundleProject is the pseudo-project path output bundles are stored under, keeping
// them in a namespace no real project can occupy (a project path is workspace-
// relative and never begins with "@"). The remote backend sees an ordinary
// (project, key) pair and needs no new capability.
const bundleProject = "@outputs"

// bundleManifestName is the bundle's descriptor member. Named distinctly from the
// artifact's "manifests/..." tree so no reader can confuse the two object types.
const bundleManifestName = "output.json"

// OutputBundle is the metadata half of a published output: the run's descriptor plus
// the key lines behind it. The captured bytes travel beside it as a separate member.
type OutputBundle struct {
	Schema     int              `json:"schema"`
	Descriptor OutputDescriptor `json:"descriptor"`
	KeyLines   []string         `json:"key_lines,omitempty"`
}

// bundleSchema is the OutputBundle wire version.
const bundleSchema = 1

// bundleOutputName is the member holding the captured output bytes verbatim.
const bundleOutputName = "output.log"

// PublishOutput uploads the run behind ref as a signed output bundle, so another
// machine can resolve that exact ref. Passing runs already travel with the cache
// artifact; this is what makes a FAILING run shareable, and it is always explicit.
// It errors when no remote backend is configured, when the backend is inactive, or
// when the cache holds no signing key (an unsigned bundle no consumer would accept).
func (c *Cache) PublishOutput(ctx context.Context, ref string) (string, error) {
	if c.remote == nil {
		return "", errors.New("cache: no remote backend configured")
	}
	if !c.remote.Active(ctx) {
		return "", errors.New("cache: remote backend is not active in this environment")
	}
	if c.signer == nil {
		return "", errors.New("cache: publishing needs a signing key (MAGUS_CACHE_SIGNING_KEY); an unsigned bundle would be refused on import")
	}
	if c.outputs == nil {
		return "", errors.New("cache: no output store")
	}
	data, desc, err := c.outputs.ByRef(ref)
	if err != nil {
		return "", err
	}
	if desc.Ref == "" {
		return "", fmt.Errorf("cache: ref %q has no descriptor to publish", ref)
	}
	lines, _ := c.outputs.KeyLinesByRef(ref) // absent is fine: the bytes are the point
	bundle := OutputBundle{Schema: bundleSchema, Descriptor: desc, KeyLines: lines}
	meta, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}

	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := writeBundle(c.signer, pw, secret.Redact(ctx, meta), secret.Redact(ctx, data))
		_ = pw.CloseWithError(err)
		errCh <- err
	}()
	putErr := c.remote.PutArtifact(ctx, bundleProject, desc.Ref, pr)
	_ = pr.CloseWithError(putErr)
	if writeErr := <-errCh; writeErr != nil {
		return "", writeErr
	}
	if putErr != nil {
		return "", putErr
	}
	return desc.Ref, nil
}

// writeBundle streams a signed gzip-tar bundle: metadata, output bytes, signature.
// The signature covers both members through the same envelope artifacts use, so a
// bundle is authenticated exactly as strictly.
func writeBundle(s *signer, w io.Writer, meta, output []byte) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	add := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: name, Size: int64(len(data)), Mode: 0o644,
		}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	if err := add(bundleManifestName, meta); err != nil {
		return err
	}
	if err := add(bundleOutputName, output); err != nil {
		return err
	}
	sum := sha256.Sum256(output)
	sig, err := s.sign(meta, map[string]string{bundleOutputName: hex.EncodeToString(sum[:])})
	if err != nil {
		return err
	}
	if err := add(sigFileName, sig); err != nil {
		return err
	}
	return errors.Join(tw.Close(), gz.Close())
}

// fetchBundle resolves a ref against the remote bundle namespace, verifying the
// signature before returning anything. It is the remote half of ByRef: consulted only
// when the local store has no such ref. A ref must be exact here - a remote store
// cannot be scanned for prefixes the way a local directory can.
func (c *Cache) fetchBundle(ctx context.Context, ref string) ([]byte, OutputDescriptor, error) {
	if c.remote == nil || !c.remote.Active(ctx) {
		return nil, OutputDescriptor{}, fs.ErrNotExist
	}
	r, err := c.remote.GetArtifact(ctx, bundleProject, ref)
	if err != nil {
		return nil, OutputDescriptor{}, err
	}
	if r == nil {
		return nil, OutputDescriptor{}, fs.ErrNotExist
	}
	defer r.Close()
	return c.readBundle(r)
}

// readBundle parses and authenticates a bundle stream. Nothing is returned unless the
// signature verifies against the trust set and covers the output bytes received.
func (c *Cache) readBundle(r io.Reader) ([]byte, OutputDescriptor, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, OutputDescriptor{}, fmt.Errorf("output bundle: gzip: %w", err)
	}
	defer gz.Close()

	var meta, output, sigBytes []byte
	budget := c.importLimit()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, OutputDescriptor{}, fmt.Errorf("output bundle: tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		buf, err := readCapped(tr, &budget)
		if err != nil {
			return nil, OutputDescriptor{}, err
		}
		switch path.Clean(hdr.Name) {
		case bundleManifestName:
			meta = buf
		case bundleOutputName:
			output = buf
		case sigFileName:
			sigBytes = buf
		}
	}
	if meta == nil {
		return nil, OutputDescriptor{}, errors.New("output bundle: no metadata member")
	}
	if c.verifier == nil {
		return nil, OutputDescriptor{}, errors.New("output bundle: no trust set configured; refusing to read an unauthenticated bundle")
	}
	if sigBytes == nil {
		return nil, OutputDescriptor{}, errors.New("output bundle: unsigned; refusing")
	}
	sum := sha256.Sum256(output)
	if err := c.verifier.verify(sigBytes, meta, map[string]string{bundleOutputName: hex.EncodeToString(sum[:])}); err != nil {
		return nil, OutputDescriptor{}, fmt.Errorf("output bundle: %w", err)
	}
	var b OutputBundle
	if err := json.Unmarshal(meta, &b); err != nil {
		return nil, OutputDescriptor{}, fmt.Errorf("output bundle: parse: %w", err)
	}
	return output, b.Descriptor, nil
}

// OutputByRef resolves a ref to its captured bytes and descriptor, consulting the
// LOCAL store first and then, only if the ref is unknown here, the remote bundle
// namespace. That order matters: local is authoritative and free, and a remote lookup
// is a network call no one asked for until the answer is not here. A miss reports
// which stores were consulted, so "not found" never hides where magus looked.
func (c *Cache) OutputByRef(ctx context.Context, ref string) ([]byte, OutputDescriptor, error) {
	if c.outputs != nil {
		data, desc, err := c.outputs.ByRef(ref)
		if err == nil {
			return data, desc, nil
		}
		var amb *AmbiguousRefError
		if errors.As(err, &amb) {
			return nil, OutputDescriptor{}, err // a local prefix collision is the user's to resolve
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, OutputDescriptor{}, err
		}
	}
	if c.remote == nil {
		return nil, OutputDescriptor{}, fs.ErrNotExist
	}
	data, desc, err := c.fetchBundle(ctx, ref)
	if err == nil {
		return data, desc, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, OutputDescriptor{}, &RefNotFoundError{Ref: ref, Stores: []string{"local cache", "remote published outputs"}}
	}
	return nil, OutputDescriptor{}, err
}

// RefNotFoundError reports a ref that resolved in none of the stores consulted, and
// names them. "Not found" is only actionable if the reader knows where magus looked -
// a foreign ref that was never published looks identical to a mistyped one otherwise.
type RefNotFoundError struct {
	Ref    string
	Stores []string
}

func (e *RefNotFoundError) Error() string {
	return fmt.Sprintf("no stored output for ref %q; consulted: %s", e.Ref, strings.Join(e.Stores, ", "))
}

// Is reports RefNotFoundError as fs.ErrNotExist, so existing not-found handling
// (the CLI's MGS8001 path) keeps working while the message gains the store list.
func (e *RefNotFoundError) Is(target error) bool { return target == fs.ErrNotExist }
