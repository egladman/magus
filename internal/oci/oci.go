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
// whole protocol used here is six requests, and the clients that wrap it bring an
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
	"regexp"
	"slices"
	"strings"
	"sync"

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
	ref, err := parseName(s)
	if err != nil {
		return Reference{}, err
	}
	if ref.Tag == "" && ref.Digest == "" {
		return Reference{}, fmt.Errorf("oci: %q names no tag; write <registry>/<repository>:<tag> or @sha256:<digest>", s)
	}
	return ref, nil
}

// ParseRepository reads "<registry>/<repository>", the form a tag listing addresses. A
// tag or digest is refused rather than ignored: a listing reads every tag, and a
// reference that looked narrower would say otherwise.
func ParseRepository(s string) (Reference, error) {
	ref, err := parseName(s)
	if err != nil {
		return Reference{}, err
	}
	if ref.Tag != "" || ref.Digest != "" {
		return Reference{}, fmt.Errorf("oci: %q names a tag or digest; a repository is <registry>/<repository>", s)
	}
	return ref, nil
}

func parseName(s string) (Reference, error) {
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
		if err := ValidateTag(tag); err != nil {
			return Reference{}, fmt.Errorf("oci: %q: %w", s, err)
		}
	}
	host, repo, ok := strings.Cut(name, "/")
	if !ok || host == "" || repo == "" || !isRegistryHost(host) {
		return Reference{}, fmt.Errorf("oci: %q names no registry host; write <registry>/<repository>:<tag>", s)
	}
	return Reference{Registry: host, Repository: repo, Tag: tag, Digest: d}, nil
}

// tagPattern is the distribution spec's tag grammar.
var tagPattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)

// ValidateTag reports whether tag is one a registry accepts. Checked before any upload,
// so a push with a bad second tag fails before it has written the first.
func ValidateTag(tag string) error {
	if tag == "" {
		return errors.New("empty tag")
	}
	if !tagPattern.MatchString(tag) {
		return fmt.Errorf("tag %q is not [A-Za-z0-9_][A-Za-z0-9._-]{0,127}", tag)
	}
	return nil
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

// Client talks to registries. The zero value is usable and pulls anonymously. It is
// safe for concurrent use, and must not be copied after first use.
type Client struct {
	// HTTP is the transport; nil means http.DefaultClient.
	HTTP *http.Client
	// Username and Password authenticate every request, pull included, so a private
	// repository reads the same way it is written. Both empty is the anonymous path,
	// which is all a reader of a public repository needs. Password is never formatted
	// into an error.
	Username string
	Password string

	mu         sync.Mutex
	challenges map[string]challenge // by registry host
	tokens     map[tokenKey]string  // Authorization values by (realm, service, scope)
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
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
	auth   string
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
	auth, err := c.authorize(ctx, ref, "pull")
	if err != nil {
		return nil, err
	}
	m, raw, err := c.manifest(ctx, ref, auth)
	if err != nil {
		return nil, err
	}
	if wantArtifactType != "" && m.ArtifactType != wantArtifactType {
		return nil, fmt.Errorf("oci: %s carries artifactType %q, wanted %q", ref, m.ArtifactType, wantArtifactType)
	}
	return &Artifact{Manifest: m, Raw: raw, client: c, ref: ref, auth: auth}, nil
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
	blob, err := a.client.blob(ctx, a.ref, a.auth, desc)
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

func (c *Client) manifest(ctx context.Context, ref Reference, auth string) (ocispec.Manifest, []byte, error) {
	req, err := c.newGetRequest(ctx, ref, auth, "/manifests/"+ref.manifestRef())
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
func (c *Client) blob(ctx context.Context, ref Reference, auth string, want ocispec.Descriptor) ([]byte, error) {
	req, err := c.newGetRequest(ctx, ref, auth, "/blobs/"+want.Digest.String())
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
func (c *Client) newGetRequest(ctx context.Context, ref Reference, auth, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base(ref)+path, nil)
	if err != nil {
		return nil, err
	}
	setAuth(req, auth)
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

// Content is what one artifact carries: its type, its manifest annotations, and its
// layers in order.
type Content struct {
	ArtifactType string
	// Annotations become the manifest's own annotations. Keys encode sorted, so equal
	// maps always encode to the same manifest bytes.
	Annotations map[string]string
	Layers      []Layer
}

// Manifest encodes the manifest Push uploads for c and returns it with its digest. It
// touches no network, so a caller learns the digest a push would produce before, or
// instead of, pushing.
func (c Content) Manifest() ([]byte, digest.Digest, error) {
	if len(c.Layers) == 0 {
		return nil, "", errors.New("oci: no layers; a manifest with none is not a valid artifact")
	}
	descs := make([]ocispec.Descriptor, 0, len(c.Layers))
	for _, l := range c.Layers {
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
	raw, err := json.Marshal(ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: c.ArtifactType,
		Config:       ocispec.DescriptorEmptyJSON,
		Layers:       descs,
		Annotations:  c.Annotations,
	})
	if err != nil {
		return nil, "", fmt.Errorf("oci: encode manifest: %w", err)
	}
	return raw, digest.FromBytes(raw), nil
}

// Push uploads content's blobs once and PUTs its manifest under ref.Tag and then each
// of moreTags, returning the manifest digest a pinned reference names. Every tag is
// validated before anything is uploaded.
//
// This is the only way to publish: a registry has no per-blob append, so adding a layer
// to an existing artifact means re-PUTting the manifest.
//
// It is NOT idempotent in the way a content-addressed store is: pushing to a tag that
// already exists moves the tag. That is the intent for a floating tag like "latest" and
// is why the caller, not this package, decides which tags to write.
func (c *Client) Push(ctx context.Context, ref Reference, content Content, moreTags ...string) (digest.Digest, error) {
	if ref.Tag == "" || ref.Digest != "" {
		return "", fmt.Errorf("oci: push %s: name a tag and no digest; a push writes a tag, and a digest is what it returns", ref)
	}
	tags := []string{ref.Tag}
	for _, t := range moreTags {
		if err := ValidateTag(t); err != nil {
			return "", fmt.Errorf("oci: push %s: %w", ref, err)
		}
		if !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	raw, d, err := content.Manifest()
	if err != nil {
		return "", fmt.Errorf("oci: push %s: %w", ref, err)
	}
	auth, err := c.authorize(ctx, ref, "push,pull")
	if err != nil {
		return "", err
	}
	// The empty config blob must exist before a manifest may reference it, even though
	// every artifact in every repository shares the same two bytes.
	if err := c.putBlob(ctx, ref, auth, emptyConfigPayload); err != nil {
		return "", err
	}
	for _, l := range content.Layers {
		if err := c.putBlob(ctx, ref, auth, l.Payload); err != nil {
			return "", err
		}
	}
	for _, tag := range tags {
		if err := c.putManifest(ctx, ref, auth, tag, raw); err != nil {
			return "", err
		}
	}
	return d, nil
}

// Tags lists repo's tags in the order the registry serves them, following its Link
// pagination to the end. A next page on another host is refused, since the request
// carries this repository's bearer token.
func (c *Client) Tags(ctx context.Context, repo Reference) ([]string, error) {
	auth, err := c.authorize(ctx, repo, "pull")
	if err != nil {
		return nil, err
	}
	next, err := url.Parse(c.base(repo) + "/tags/list")
	if err != nil {
		return nil, err
	}
	var tags []string
	seen := map[string]bool{}
	for {
		if seen[next.String()] {
			return nil, fmt.Errorf("oci: list %s: the registry paginated back to %s", repo, next)
		}
		seen[next.String()] = true
		page, link, err := c.tagPage(ctx, repo, auth, next)
		if err != nil {
			return nil, err
		}
		tags = append(tags, page...)
		var more bool
		next, more, err = nextPage(next, link)
		if err != nil {
			return nil, fmt.Errorf("oci: list %s: %w", repo, err)
		}
		if !more {
			return tags, nil
		}
	}
}

func (c *Client) tagPage(ctx context.Context, repo Reference, auth string, u *url.URL) ([]string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	setAuth(req, auth)
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("oci: list %s: %w", repo, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("oci: list %s: %s", repo, statusLine(resp))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, registryReplyLimit))
	if err != nil {
		return nil, "", fmt.Errorf("oci: list %s: %w", repo, err)
	}
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, "", fmt.Errorf("oci: list %s: %w", repo, err)
	}
	return body.Tags, resp.Header.Get("Link"), nil
}

// nextPage reads the rel="next" target of an RFC 8288 Link header, resolved against
// the page that carried it; more is false when the header names none.
func nextPage(page *url.URL, link string) (next *url.URL, more bool, err error) {
	for part := range strings.SplitSeq(link, ",") {
		target, params, ok := strings.Cut(strings.TrimSpace(part), ";")
		if !ok || !strings.Contains(strings.ReplaceAll(params, " ", ""), `rel="next"`) {
			continue
		}
		ref, err := url.Parse(strings.Trim(strings.TrimSpace(target), "<>"))
		if err != nil {
			return nil, false, fmt.Errorf("next page %q: %w", target, err)
		}
		u := page.ResolveReference(ref)
		if u.Host != page.Host || u.Scheme != page.Scheme {
			return nil, false, fmt.Errorf("next page %s is on another origin than %s", u, page.Host)
		}
		return u, true, nil
	}
	return nil, false, nil
}

// putBlob uploads one blob through the two-step upload the v2 API defines: ask for a
// session, then PUT the bytes with the digest. A blob the registry already holds is
// skipped, which is what makes re-publishing an unchanged graph nearly free.
func (c *Client) putBlob(ctx context.Context, ref Reference, auth string, payload []byte) error {
	d := digest.FromBytes(payload)
	have, err := c.blobExists(ctx, ref, auth, d)
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
	setAuth(req, auth)
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
	setAuth(put, auth)
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

func (c *Client) blobExists(ctx context.Context, ref Reference, auth string, d digest.Digest) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.base(ref)+"/blobs/"+d.String(), nil)
	if err != nil {
		return false, err
	}
	setAuth(req, auth)
	resp, err := c.http().Do(req)
	if err != nil {
		return false, fmt.Errorf("oci: probe %s: %w", d, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

func (c *Client) putManifest(ctx context.Context, ref Reference, auth, tag string, body []byte) error {
	dest := ref
	dest.Tag = tag
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base(ref)+"/manifests/"+tag, bytes.NewReader(body))
	if err != nil {
		return err
	}
	setAuth(req, auth)
	req.Header.Set("Content-Type", ocispec.MediaTypeImageManifest)
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("oci: tag %s: %w", dest, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("oci: tag %s: %s", dest, statusLine(resp))
	}
	return nil
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
