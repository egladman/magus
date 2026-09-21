// Package oci publishes and fetches named blobs as an OCI artifact, over the registry
// v2 HTTP API.
//
// It exists so a workspace can hand its knowledge graph to everyone else who works on the
// repository without anyone rebuilding it. A registry is the right store for that: it is
// already content-addressed, it already has a public read path, and a team that can clone
// the repository can already reach its packages. Published spells travel the same way
// (internal/spell/remote), pulled by manifest digest rather than by tag.
//
// An artifact carries SEVERAL layers, each titled, so a reader fetches the one piece it
// is missing rather than the whole. The title is the spec's own
// org.opencontainers.image.title annotation, which is what other OCI tooling already
// reads as a layer's filename.
//
// The SHAPES come from the spec (image-spec's own Manifest, Descriptor, media types and
// DescriptorEmptyJSON; go-digest's Digest), so nothing here transcribes a constant the
// spec already fixes. The TRANSPORT is net/http directly rather than a registry SDK: the
// whole protocol used here is five requests, and the clients that wrap it bring an
// OpenPGP stack this tool has no other use for, which is future govulncheck surface on a
// gate that already runs it.
//
// What this does NOT do is the rest of OCI: no image config, no multi-arch index, no
// referrers (ghcr.io does not implement the referrers API), no cross-repository mount, no
// chunked upload. A caller that needs those wants a real client, not this.
package oci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// registryReplyLimit bounds a manifest or token response read into memory. A registry
// answering either with megabytes is one to stop reading, not to trust.
const registryReplyLimit = 4 << 20

// emptyConfigPayload is the config blob's content, the two bytes
// ocispec.DescriptorEmptyJSON describes. Registries require the blob to EXIST even though
// every artifact shares it, so a push uploads it like any other layer.
var emptyConfigPayload = []byte("{}")

// Reference names one artifact: a registry host, a repository path, and a tag, a
// manifest digest, or both.
type Reference struct {
	Registry   string // e.g. "ghcr.io"
	Repository string // e.g. "egladman/magus/knowledge-graph"
	Tag        string // e.g. "latest"
	// Digest, when set, is what a pull addresses instead of Tag, and the fetched
	// manifest must hash to it. A tag can move; a digest names one manifest forever.
	Digest digest.Digest
}

// ParseReference reads "ghcr.io/owner/name/artifact:tag", "...artifact@sha256:<hex>", or
// both at once. A tag or a digest is required rather than defaulted to "latest": a
// publish that silently retagged latest because someone omitted a tag is the kind of
// mistake a registry cannot undo.
//
// The tag separator is the last colon AFTER the last path separator, because a registry
// may carry a port. Cutting on the first colon reads localhost:5000/team/graph:v1 as host
// "localhost" with the rest as a tag, so a ported registry cannot be addressed at all.
func ParseReference(s string) (Reference, error) {
	name, rawDigest, hasDigest := strings.Cut(s, "@")
	var d digest.Digest
	if hasDigest {
		d = digest.Digest(rawDigest)
		if err := d.Validate(); err != nil {
			return Reference{}, fmt.Errorf("oci: %q carries an unusable digest: %w", s, err)
		}
	}
	var tag string
	if colon := strings.LastIndex(name, ":"); colon >= 0 && colon > strings.LastIndex(name, "/") {
		name, tag = name[:colon], name[colon+1:]
		if tag == "" {
			return Reference{}, fmt.Errorf("oci: %q has an empty tag after its colon", s)
		}
	}
	if tag == "" && !hasDigest {
		return Reference{}, fmt.Errorf("oci: %q names no tag; write <registry>/<repository>:<tag> or @sha256:<digest>", s)
	}
	host, repo, ok := strings.Cut(name, "/")
	if !ok || host == "" || repo == "" || !isRegistryHost(host) {
		return Reference{}, fmt.Errorf("oci: %q names no registry host; write <registry>/<repository>:<tag>", s)
	}
	return Reference{Registry: host, Repository: repo, Tag: tag, Digest: d}, nil
}

// isRegistryHost distinguishes a registry from the first segment of a bare repository
// path, the way a container runtime does: a dot, a port, or the literal "localhost".
// Without it "egladman/magus:v1" would read as a host named "egladman".
func isRegistryHost(host string) bool {
	return strings.Contains(host, ".") || strings.Contains(host, ":") || host == "localhost"
}

func (r Reference) String() string {
	s := r.Registry + "/" + r.Repository
	if r.Tag != "" {
		s += ":" + r.Tag
	}
	if r.Digest != "" {
		s += "@" + r.Digest.String()
	}
	return s
}

// manifestRef is the path segment a manifest GET addresses: the digest when there is one.
func (r Reference) manifestRef() string {
	if r.Digest != "" {
		return r.Digest.String()
	}
	return r.Tag
}

// Client talks to one registry. The zero value is usable and pulls anonymously.
type Client struct {
	// HTTP is the transport; nil means http.DefaultClient.
	HTTP *http.Client
	// Username and Password authenticate a PUSH. Both empty is the anonymous pull path,
	// which is the only path a reader ever needs for a public repository.
	Username string
	Password string
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// token fetches a bearer token for one scope.
//
// Anonymous pull of a PUBLIC repository still needs this: the registry answers an
// unauthenticated /v2/ request with 401 and expects the client to come back with a token
// it will hand to anybody. So "no credentials" is not the same as "no token", and a
// client that skips this step reads a public artifact as forbidden.
func (c *Client) token(ctx context.Context, ref Reference, actions string) (string, error) {
	q := url.Values{
		"service": {ref.Registry},
		"scope":   {"repository:" + ref.Repository + ":" + actions},
	}
	endpoint := "https://" + ref.Registry + "/token?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	if c.Username != "" || c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return "", fmt.Errorf("oci: token for %s: %w", ref.Repository, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oci: token for %s: %s", ref.Repository, statusLine(resp))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, registryReplyLimit))
	if err != nil {
		return "", fmt.Errorf("oci: token for %s: %w", ref.Repository, err)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", fmt.Errorf("oci: token for %s: %w", ref.Repository, err)
	}
	if body.Token != "" {
		return body.Token, nil
	}
	if body.AccessToken != "" {
		return body.AccessToken, nil
	}
	return "", fmt.Errorf("oci: token for %s: the registry returned no token", ref.Repository)
}

// Artifact is one pull in progress: the manifest, plus the token that fetched it. A
// reader that wants several layers holds one of these, so neither the token exchange nor
// the manifest GET is repeated per layer.
//
// It is NOT safe for concurrent use. Nothing here mutates, but the token expires, and a
// handle that outlives its lifetime returns 401 on the next Blob rather than refreshing.
type Artifact struct {
	// Manifest is the fetched manifest, exposed so a caller can walk descriptors that
	// Layer's title lookup does not reach.
	Manifest ocispec.Manifest
	// Raw is the manifest exactly as served, the bytes a manifest digest is computed
	// over. Re-encoding Manifest does not reproduce them.
	Raw []byte

	client *Client
	ref    Reference
	token  string
}

// ErrLayerMiss reports that the manifest carries no layer with the requested title. A
// caller deciding between fetching and rebuilding needs this distinguishable from a
// transport failure, so it is a sentinel rather than a formatted error.
var ErrLayerMiss = errors.New("oci: no layer with that title")

// Artifact fetches ref's manifest and returns a handle for reading its layers. It reads
// no layer, so an artifact whose layers are large costs one manifest GET to inspect.
//
// wantArtifactType, when non-empty, is checked before the handle is returned: a tag
// someone else's tooling pushed to is the ordinary way to get bytes that parse as JSON
// and mean something entirely different, and the artifact type is the only field that
// says what the blob was meant to be.
func (c *Client) Artifact(ctx context.Context, ref Reference, wantArtifactType string) (*Artifact, error) {
	tok, err := c.token(ctx, ref, "pull")
	if err != nil {
		return nil, err
	}
	m, raw, err := c.manifest(ctx, ref, tok)
	if err != nil {
		return nil, err
	}
	if wantArtifactType != "" && m.ArtifactType != wantArtifactType {
		return nil, fmt.Errorf("oci: %s carries artifactType %q, wanted %q", ref, m.ArtifactType, wantArtifactType)
	}
	return &Artifact{Manifest: m, Raw: raw, client: c, ref: ref, token: tok}, nil
}

// ErrManifestDigest reports a manifest fetched by digest whose bytes hash to something
// else. The registry served a different manifest than the one the reference pins.
var ErrManifestDigest = errors.New("oci: manifest does not match the digest its reference pins")

// ErrBlobDigest reports a layer whose bytes do not hash to the digest its manifest
// names.
var ErrBlobDigest = errors.New("oci: layer does not match the digest its manifest names")

// Find returns the descriptor of the layer titled name, and whether there is one. The
// first match wins; a publisher that titled two layers alike has already lost the ability
// to address either.
func (a *Artifact) Find(name string) (ocispec.Descriptor, bool) {
	for _, d := range a.Manifest.Layers {
		if d.Annotations[ocispec.AnnotationTitle] == name {
			return d, true
		}
	}
	return ocispec.Descriptor{}, false
}

// Layer fetches the layer titled name, or wraps ErrLayerMiss when the manifest has none.
func (a *Artifact) Layer(ctx context.Context, name string) ([]byte, error) {
	d, ok := a.Find(name)
	if !ok {
		return nil, fmt.Errorf("%w: %q in %s", ErrLayerMiss, name, a.ref)
	}
	return a.Blob(ctx, d)
}

// Blob fetches one layer by its descriptor, bounded by the size the descriptor declares
// and verified against the digest it names. desc must come from this artifact's own
// manifest: a descriptor from anywhere else names a blob this repository need not hold,
// and the size bound would be whatever that other manifest claimed.
func (a *Artifact) Blob(ctx context.Context, desc ocispec.Descriptor) ([]byte, error) {
	// Validated before Verifier, which panics on a digest whose algorithm this build has
	// no hash for. The manifest is remote input, so that is a reachable panic.
	if err := desc.Digest.Validate(); err != nil {
		return nil, fmt.Errorf("oci: %s names an unusable layer digest %q: %w", a.ref, desc.Digest, err)
	}
	blob, err := a.client.blob(ctx, a.ref, a.token, desc)
	if err != nil {
		return nil, err
	}
	// The digest is the registry's contract, so verifying it is what makes a pull over a
	// plain HTTP hop trustworthy without signing anything.
	verifier := desc.Digest.Verifier()
	if _, err := verifier.Write(blob); err != nil {
		return nil, fmt.Errorf("oci: verify %s layer: %w", a.ref, err)
	}
	if !verifier.Verified() {
		return nil, fmt.Errorf("%w: %s (%s)", ErrBlobDigest, a.ref, desc.Digest)
	}
	return blob, nil
}

func (c *Client) manifest(ctx context.Context, ref Reference, tok string) (ocispec.Manifest, []byte, error) {
	req, err := c.newGetRequest(ctx, ref, tok, "/manifests/"+ref.manifestRef())
	if err != nil {
		return ocispec.Manifest{}, nil, err
	}
	req.Header.Set("Accept", ocispec.MediaTypeImageManifest)
	resp, err := c.http().Do(req)
	if err != nil {
		return ocispec.Manifest{}, nil, fmt.Errorf("oci: fetch %s: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ocispec.Manifest{}, nil, fmt.Errorf("oci: fetch %s: %s", ref, statusLine(resp))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, registryReplyLimit))
	if err != nil {
		return ocispec.Manifest{}, nil, fmt.Errorf("oci: fetch %s: %w", ref, err)
	}
	// Checked before decoding: a pinned pull trusts nothing the registry says until the
	// bytes are the ones the pin names.
	if ref.Digest != "" {
		// FromBytes panics on an algorithm this build cannot hash.
		if err := ref.Digest.Validate(); err != nil {
			return ocispec.Manifest{}, nil, fmt.Errorf("oci: %s: %w", ref, err)
		}
		if got := ref.Digest.Algorithm().FromBytes(raw); got != ref.Digest {
			return ocispec.Manifest{}, nil, fmt.Errorf("%w: %s served %s", ErrManifestDigest, ref, got)
		}
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return ocispec.Manifest{}, nil, fmt.Errorf("oci: decode %s manifest: %w", ref, err)
	}
	return m, raw, nil
}

// blob fetches one layer, bounded by the size its own descriptor declares. The bound is
// free (the manifest already stated it) and without it a registry answering a small
// descriptor with an endless body reads until the process dies.
func (c *Client) blob(ctx context.Context, ref Reference, tok string, want ocispec.Descriptor) ([]byte, error) {
	req, err := c.newGetRequest(ctx, ref, tok, "/blobs/"+want.Digest.String())
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("oci: fetch %s blob: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oci: fetch %s blob: %s", ref, statusLine(resp))
	}
	// One byte past the declared size, so an overlong body is caught rather than silently
	// truncated into a digest mismatch that blames the wrong thing.
	blob, err := io.ReadAll(io.LimitReader(resp.Body, want.Size+1))
	if err != nil {
		return nil, fmt.Errorf("oci: fetch %s blob: %w", ref, err)
	}
	if int64(len(blob)) != want.Size {
		return nil, fmt.Errorf("oci: %s layer is %d bytes, manifest declared %d", ref, len(blob), want.Size)
	}
	return blob, nil
}

// newGetRequest BUILDS a request; it does not issue one. Named for that, because its
// siblings putBlob and putManifest do perform their verb, and a `get` beside them teaches
// a reader that all three round-trip.
func (c *Client) newGetRequest(ctx context.Context, ref Reference, tok, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base(ref)+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return req, nil
}

func (c *Client) base(ref Reference) string {
	return "https://" + ref.Registry + "/v2/" + ref.Repository
}

// Layer is one named payload in an artifact. Name becomes the layer descriptor's title
// annotation, which is how a puller addresses it; an empty Name leaves the layer
// unaddressable by title, which is only sensible for a single-layer artifact.
type Layer struct {
	Name      string
	MediaType string
	Payload   []byte
}

// Push uploads every layer and tags one manifest naming them, in the order given.
//
// This is the only way to publish: a registry has no per-blob append, so adding a layer
// to an existing artifact means re-PUTting the manifest. A caller that accumulates layers
// one at a time must batch them here rather than expect an incremental write.
//
// It is NOT idempotent in the way a content-addressed store is: pushing to a tag that
// already exists moves the tag. That is the intent for a floating tag like "latest" and
// is why the caller, not this package, decides which tags to write.
//
// It returns the manifest's digest, which is what a pinned reference names.
func (c *Client) Push(ctx context.Context, ref Reference, artifactType string, layers ...Layer) (digest.Digest, error) {
	if len(layers) == 0 {
		return "", fmt.Errorf("oci: push %s: no layers; a manifest with none is not a valid artifact", ref)
	}
	if ref.Tag == "" || ref.Digest != "" {
		return "", fmt.Errorf("oci: push %s: name a tag and no digest; a push writes a tag, and a digest is what it returns", ref)
	}
	tok, err := c.token(ctx, ref, "push,pull")
	if err != nil {
		return "", err
	}
	// The empty config blob must exist before a manifest may reference it, even though
	// every artifact in every repository shares the same two bytes.
	if err := c.putBlob(ctx, ref, tok, emptyConfigPayload); err != nil {
		return "", err
	}
	descs := make([]ocispec.Descriptor, 0, len(layers))
	for _, l := range layers {
		if err := c.putBlob(ctx, ref, tok, l.Payload); err != nil {
			return "", err
		}
		d := ocispec.Descriptor{
			MediaType: l.MediaType,
			Digest:    digest.FromBytes(l.Payload),
			Size:      int64(len(l.Payload)),
		}
		if l.Name != "" {
			d.Annotations = map[string]string{ocispec.AnnotationTitle: l.Name}
		}
		descs = append(descs, d)
	}
	m := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: artifactType,
		Config:       ocispec.DescriptorEmptyJSON,
		Layers:       descs,
	}
	return c.putManifest(ctx, ref, tok, m)
}

// putBlob uploads one blob through the two-step upload the v2 API defines: ask for a
// session, then PUT the bytes with the digest. A blob the registry already holds is
// skipped, which is what makes re-publishing an unchanged graph nearly free.
func (c *Client) putBlob(ctx context.Context, ref Reference, tok string, payload []byte) error {
	d := digest.FromBytes(payload)
	have, err := c.blobExists(ctx, ref, tok, d)
	if err != nil {
		return err
	}
	if have {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base(ref)+"/blobs/uploads/", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("oci: start upload to %s: %w", ref.Repository, err)
	}
	// Closed AFTER the status check, not before: statusLine reads the body for whatever
	// the registry said, and closing first discards the explanation of the very first
	// error a misconfigured push hits.
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("oci: start upload to %s: %s", ref.Repository, statusLine(resp))
	}
	location := resp.Header.Get("Location")
	if location == "" {
		return fmt.Errorf("oci: start upload to %s: the registry returned no upload location", ref.Repository)
	}

	// The location may be absolute or registry-relative, and it may already carry a query.
	upload, err := url.Parse(location)
	if err != nil {
		return fmt.Errorf("oci: upload location %q: %w", location, err)
	}
	if !upload.IsAbs() {
		upload.Scheme, upload.Host = "https", ref.Registry
	}
	q := upload.Query()
	q.Set("digest", d.String())
	upload.RawQuery = q.Encode()

	put, err := http.NewRequestWithContext(ctx, http.MethodPut, upload.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	put.Header.Set("Authorization", "Bearer "+tok)
	put.Header.Set("Content-Type", "application/octet-stream")
	put.ContentLength = int64(len(payload))
	done, err := c.http().Do(put)
	if err != nil {
		return fmt.Errorf("oci: upload %s: %w", d, err)
	}
	defer done.Body.Close()
	if done.StatusCode != http.StatusCreated {
		return fmt.Errorf("oci: upload %s: %s", d, statusLine(done))
	}
	return nil
}

func (c *Client) blobExists(ctx context.Context, ref Reference, tok string, d digest.Digest) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.base(ref)+"/blobs/"+d.String(), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.http().Do(req)
	if err != nil {
		return false, fmt.Errorf("oci: probe %s: %w", d, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

func (c *Client) putManifest(ctx context.Context, ref Reference, tok string, m ocispec.Manifest) (digest.Digest, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base(ref)+"/manifests/"+ref.Tag, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", ocispec.MediaTypeImageManifest)
	resp, err := c.http().Do(req)
	if err != nil {
		return "", fmt.Errorf("oci: tag %s: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("oci: tag %s: %s", ref, statusLine(resp))
	}
	return digest.FromBytes(body), nil
}

// statusLine renders a failed response for a human: the status plus whatever the registry
// said about it, bounded, because an error body can be a whole HTML page.
func statusLine(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		return resp.Status
	}
	return resp.Status + ": " + detail
}
