package oci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
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
		return Reference{}, fmt.Errorf("oci: %q names no tag, write <registry>/<repository>:<tag> or @sha256:<digest>", s)
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
		return Reference{}, fmt.Errorf("oci: %q names a tag or digest, a repository is <registry>/<repository>", s)
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
			return Reference{}, fmt.Errorf("%q: %w", s, err)
		}
	}
	host, repo, ok := strings.Cut(name, "/")
	if !ok || host == "" || repo == "" || !isRegistryHost(host) {
		return Reference{}, fmt.Errorf("oci: %q names no registry host, write <registry>/<repository>:<tag>", s)
	}
	if dockerHubHosts[host] && !strings.Contains(repo, "/") {
		return Reference{}, fmt.Errorf("oci: %q names a Docker Hub repository with no namespace: an official image is %s/library/%s", s, host, repo)
	}
	return Reference{Registry: host, Repository: repo, Tag: tag, Digest: d}, nil
}

// dockerHubHosts are the names people write for Docker Hub. Neither serves the
// distribution API: docker.io answers /v2/ with a redirect to www.docker.com, so every
// request goes to dockerHubEndpoint instead.
var dockerHubHosts = map[string]bool{"docker.io": true, "index.docker.io": true}

const dockerHubEndpoint = "registry-1.docker.io"

// endpoint is the host requests for r go to: Registry, except that a Docker Hub name
// maps to the host that serves its API. Registry keeps what the person wrote.
func (r Reference) endpoint() string {
	if dockerHubHosts[r.Registry] {
		return dockerHubEndpoint
	}
	return r.Registry
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
	s := fmt.Sprintf("%s/%s", r.Registry, r.Repository)
	if r.Tag != "" {
		s += ":" + r.Tag
	}
	if r.Digest != "" {
		s += "@" + r.Digest.String()
	}
	return s
}

// MarshalText renders r as String does, so a Reference encodes as the string a person
// writes.
func (r Reference) MarshalText() ([]byte, error) {
	if r.Registry == "" || r.Repository == "" {
		return nil, errors.New("oci: marshal an empty reference")
	}
	return []byte(r.String()), nil
}

// UnmarshalText parses text with ParseReference, so a tag or a digest is required.
func (r *Reference) UnmarshalText(text []byte) error {
	parsed, err := ParseReference(string(text))
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}

// repository drops the tag and digest: the address blob and manifest PUTs share.
func (r Reference) repository() Reference {
	return Reference{Registry: r.Registry, Repository: r.Repository}
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
	// HTTP is the transport; nil means a default client. Either way redirects follow
	// httpx.GuardRedirect unless HTTP sets its own CheckRedirect.
	HTTP *http.Client
	// Credentials authenticate every request, pull included, so a private repository
	// reads the same way it is written. The zero value is the anonymous path, which is
	// all a reader of a public repository needs.
	Credentials Credentials

	mu         sync.Mutex
	challenges map[string]challenge      // by registry host
	tokens     map[tokenKey]types.Secret // bearer tokens by (realm, service, scope)
}

// Credentials is a registry login. The password is a [types.Secret], revealed only where
// the Authorization header or the token request is built.
type Credentials struct {
	Username string
	Password types.Secret
}

func (c Credentials) isZero() bool { return c.Username == "" && c.Password.IsZero() }

var defaultHTTP = &http.Client{CheckRedirect: httpx.GuardRedirect}

func (c *Client) http() *http.Client {
	if c.HTTP == nil {
		return defaultHTTP
	}
	if c.HTTP.CheckRedirect != nil {
		return c.HTTP
	}
	guarded := *c.HTTP
	guarded.CheckRedirect = httpx.GuardRedirect
	return &guarded
}

// offline repeats config.Offline: internal/config imports this package, so this one
// cannot import it back. Keep the two in step.
func offline() bool {
	v := os.Getenv("MAGUS_OFFLINE")
	return v != "" && v != "0" && v != "false"
}

// do sends req through the client. Every request this package makes goes through here,
// so the offline check precedes all of them.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	if offline() {
		return nil, fmt.Errorf("oci %s %s: MAGUS_OFFLINE is set, so no request is sent: %w", req.Method, req.URL.Redacted(), ErrOffline)
	}
	return c.http().Do(req)
}

// send builds and sends one request authorized for actions on ref's repository. A 401
// that follows a cached bearer token means the token expired, so it is dropped and the
// request is sent once more with a fresh one. A 429 whose Retry-After is at most
// maxRetryAfter is sent again after that wait, up to maxRateLimitRetries times; any other
// 429 is returned for statusError to report.
func (c *Client) send(ctx context.Context, ref Reference, actions string, build func(auth string) (*http.Request, error)) (*http.Response, error) {
	refreshed, limited := false, 0
	for {
		auth, err := c.authorize(ctx, ref, actions)
		if err != nil {
			return nil, err
		}
		req, err := build(auth)
		if err != nil {
			return nil, err
		}
		resp, err := c.do(req)
		if err != nil {
			return nil, err
		}
		reportWarnings(ctx, ref, resp)
		if resp.StatusCode == http.StatusUnauthorized && !refreshed && c.forget(ref, actions, auth) {
			refreshed = true
			resp.Body.Close()
			continue
		}
		if wait, ok := retryAfter(resp); ok && limited < maxRateLimitRetries {
			limited++
			resp.Body.Close()
			if err := sleep(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		return resp, nil
	}
}

const (
	maxRateLimitRetries = 3
	maxRetryAfter       = time.Minute
)

// retryAfter reads a 429's Retry-After, in seconds or as an HTTP date. ok is false for
// any other status, a missing or unreadable header, or a wait over maxRetryAfter: a
// registry asking for an hour is answered with the error, not a hung build.
func retryAfter(resp *http.Response) (time.Duration, bool) {
	if resp.StatusCode != http.StatusTooManyRequests {
		return 0, false
	}
	h := resp.Header.Get("Retry-After")
	var wait time.Duration
	if secs, err := strconv.Atoi(h); err == nil {
		wait = time.Duration(secs) * time.Second
	} else if at, err := http.ParseTime(h); err == nil {
		wait = time.Until(at)
	} else {
		return 0, false
	}
	return max(wait, 0), wait <= maxRetryAfter
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// reportWarnings logs each `Warning: 299 - "<text>"` header, which the distribution
// spec says a client SHOULD show: a registry announces deprecations and quota changes
// this way and nowhere else.
func reportWarnings(ctx context.Context, ref Reference, resp *http.Response) {
	for _, h := range resp.Header.Values("Warning") {
		text, ok := parseWarning(h)
		if !ok {
			continue
		}
		slog.WarnContext(ctx, "the registry sent a warning", "registry", ref.Registry, "repository", ref.Repository, "warning", text)
	}
}

// parseWarning reads the text of a 299 warning; the spec defines no other code for
// registries, and the agent field is always "-".
func parseWarning(h string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(h), "299 ")
	if !ok {
		return "", false
	}
	_, quoted, ok := strings.Cut(rest, " ")
	if !ok {
		return "", false
	}
	text, err := strconv.Unquote(strings.TrimSpace(quoted))
	if err != nil {
		return "", false
	}
	return text, true
}

// Artifact is one pull in progress: the manifest, plus the token that fetched it. A
// reader that wants several layers holds one of these, so neither the token exchange nor
// the manifest GET is repeated per layer.
//
// It is NOT safe for concurrent use.
type Artifact struct {
	// Manifest is the fetched manifest, exposed so a caller can walk descriptors that
	// Layer's title lookup does not reach.
	Manifest ocispec.Manifest
	// Raw is the manifest exactly as served, the bytes a manifest digest is computed
	// over. Re-encoding Manifest does not reproduce them.
	Raw []byte

	client *Client
	ref    Reference
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
	m, raw, err := c.manifest(ctx, ref)
	if err != nil {
		return nil, err
	}
	if wantArtifactType != "" && m.ArtifactType != wantArtifactType {
		return nil, fmt.Errorf("oci: %s carries artifactType %q, wanted %q", ref, m.ArtifactType, wantArtifactType)
	}
	return &Artifact{Manifest: m, Raw: raw, client: c, ref: ref}, nil
}

// ErrManifestDigest reports a manifest fetched by digest whose bytes hash to something
// else. The registry served a different manifest than the one the reference pins.
var ErrManifestDigest = errors.New("oci: manifest does not match the digest its reference pins")

// ErrBlobDigest reports a blob whose bytes do not hash to the digest its descriptor
// names.
var ErrBlobDigest = errors.New("oci: blob does not match the digest its descriptor names")

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
		return nil, fmt.Errorf("%q in %s: %w", name, a.ref, ErrLayerMiss)
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
	blob, err := a.client.blob(ctx, a.ref, desc)
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
		return nil, fmt.Errorf("%s (%s): %w", a.ref, desc.Digest, ErrBlobDigest)
	}
	return blob, nil
}

func (c *Client) manifest(ctx context.Context, ref Reference) (ocispec.Manifest, []byte, error) {
	resp, err := c.send(ctx, ref, "pull", func(auth string) (*http.Request, error) {
		req, err := c.newGetRequest(ctx, ref, auth, "/manifests/"+ref.manifestRef())
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", ocispec.MediaTypeImageManifest)
		return req, nil
	})
	if err != nil {
		return ocispec.Manifest{}, nil, fmt.Errorf("fetch %s: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ocispec.Manifest{}, nil, fmt.Errorf("oci: fetch %s: %w", ref, statusError(resp))
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
			return ocispec.Manifest{}, nil, fmt.Errorf("%s served %s: %w", ref, got, ErrManifestDigest)
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
func (c *Client) blob(ctx context.Context, ref Reference, want ocispec.Descriptor) ([]byte, error) {
	resp, err := c.send(ctx, ref, "pull", func(auth string) (*http.Request, error) {
		return c.newGetRequest(ctx, ref, auth, "/blobs/"+want.Digest.String())
	})
	if err != nil {
		return nil, fmt.Errorf("fetch %s blob: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oci: fetch %s blob: %w", ref, statusError(resp))
	}
	// One byte past the declared size, so an overlong body is caught rather than silently
	// truncated into a digest mismatch that blames the wrong thing.
	blob, err := io.ReadAll(io.LimitReader(resp.Body, want.Size+1))
	if err != nil {
		return nil, fmt.Errorf("oci: fetch %s blob: %w", ref, err)
	}
	if int64(len(blob)) != want.Size {
		return nil, fmt.Errorf("oci: %s blob is %d bytes, its descriptor declares %d", ref, len(blob), want.Size)
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
	return fmt.Sprintf("https://%s/v2/%s", ref.endpoint(), ref.Repository)
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
		return nil, "", errors.New("oci: no layers, a manifest with none is not a valid artifact")
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
		return "", fmt.Errorf("oci: push %s: name a tag and no digest, a push writes a tag, and a digest is what it returns", ref)
	}
	tags := []string{ref.Tag}
	for _, t := range moreTags {
		if err := ValidateTag(t); err != nil {
			return "", fmt.Errorf("push %s: %w", ref, err)
		}
		if !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	raw, d, err := content.Manifest()
	if err != nil {
		return "", fmt.Errorf("push %s: %w", ref, err)
	}
	// The empty config blob must exist before a manifest may reference it, even though
	// every artifact in every repository shares the same two bytes.
	if err := c.putBlob(ctx, ref, emptyConfigPayload); err != nil {
		return "", err
	}
	for _, l := range content.Layers {
		if err := c.putBlob(ctx, ref, l.Payload); err != nil {
			return "", err
		}
	}
	for _, tag := range tags {
		if _, _, err := c.putManifest(ctx, ref, tag, ocispec.MediaTypeImageManifest, raw); err != nil {
			return "", err
		}
	}
	return d, nil
}

// Tags lists repo's tags in the order the registry serves them, following its Link
// pagination to the end. A next page on another host is refused, since the request
// carries this repository's bearer token.
func (c *Client) Tags(ctx context.Context, repo Reference) ([]string, error) {
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
		page, link, err := c.tagPage(ctx, repo, next)
		if err != nil {
			return nil, err
		}
		tags = append(tags, page...)
		var more bool
		next, more, err = nextPage(next, link)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", repo, err)
		}
		if !more {
			return tags, nil
		}
	}
}

func (c *Client) tagPage(ctx context.Context, repo Reference, u *url.URL) ([]string, string, error) {
	resp, err := c.send(ctx, repo, "pull", func(auth string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		setAuth(req, auth)
		return req, nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("list %s: %w", repo, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("oci: list %s: %w", repo, statusError(resp))
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
func (c *Client) putBlob(ctx context.Context, ref Reference, payload []byte) error {
	d := ocispec.Descriptor{Digest: digest.FromBytes(payload), Size: int64(len(payload))}
	return c.pushBlob(ctx, ref, d, bytes.NewReader(payload))
}

func (c *Client) blobExists(ctx context.Context, ref Reference, actions string, d digest.Digest) (bool, error) {
	resp, err := c.send(ctx, ref, actions, func(auth string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, fmt.Sprintf("%s/blobs/%s", c.base(ref), d.String()), nil)
		if err != nil {
			return nil, err
		}
		setAuth(req, auth)
		return req, nil
	})
	if err != nil {
		return false, fmt.Errorf("probe %s: %w", d, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("oci: probe %s: %w", d, statusError(resp))
}

// putManifest PUTs body under reference (a tag or a digest) and returns the digest the
// registry confirmed: its Docker-Content-Digest header when it sends one, which must
// match the digest of body, else the computed one. subjectSeen reports the OCI-Subject
// header, which a registry serving the Referrers API sends for a manifest with a subject.
func (c *Client) putManifest(ctx context.Context, ref Reference, reference, mediaType string, body []byte) (confirmed digest.Digest, subjectSeen bool, err error) {
	dest := ref
	dest.Tag = reference
	resp, err := c.send(ctx, ref, "push,pull", func(auth string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, fmt.Sprintf("%s/manifests/%s", c.base(ref), reference), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		setAuth(req, auth)
		req.Header.Set("Content-Type", mediaType)
		return req, nil
	})
	if err != nil {
		return "", false, fmt.Errorf("tag %s: %w", dest, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", false, fmt.Errorf("oci: tag %s: %w", dest, statusError(resp))
	}
	want := digest.FromBytes(body)
	if got := resp.Header.Get("Docker-Content-Digest"); got != "" && got != want.String() {
		return "", false, fmt.Errorf("oci: tag %s: the registry confirmed %s, the manifest is %s", dest, got, want)
	}
	return want, resp.Header.Get("OCI-Subject") != "", nil
}

// ErrNotFound reports a registry answering 404: the repository, manifest or blob is not
// there. A caller choosing between pushing and failing branches on it with errors.Is.
var ErrNotFound = errors.New("oci: not found")

// ErrUnauthorized reports a registry answering 401 after any token exchange: the
// credentials are missing or wrong.
var ErrUnauthorized = errors.New("oci: unauthorized")

// ErrDenied reports a registry answering 403: the credentials were accepted and lack
// access to the repository or the action.
var ErrDenied = errors.New("oci: denied")

// ErrTooManyRequests reports a registry answering 429 after any retry Retry-After
// allowed: a rate limit, such as Docker Hub's anonymous pull limit.
var ErrTooManyRequests = errors.New("oci: too many requests")

// ErrOffline reports a request refused before it was sent because MAGUS_OFFLINE is set.
var ErrOffline = errors.New("oci: offline")

// StatusError is a registry's non-success answer. Errors holds the distribution spec's
// error body when the registry sent one; Body holds any other body, bounded, because an
// error page can be a whole HTML document. It unwraps to ErrNotFound, ErrUnauthorized,
// ErrDenied or ErrTooManyRequests when the status is one of theirs.
type StatusError struct {
	StatusCode int
	Status     string
	Errors     []RegistryError
	Body       string
	// RetryAfter is the 429 answer's Retry-After header as sent, empty otherwise.
	RetryAfter string
}

// RegistryError is one member of the distribution spec's `{"errors": [...]}` body. Code
// is one of the spec's codes, such as MANIFEST_UNKNOWN, DENIED or TOOMANYREQUESTS, or a
// registry's own.
type RegistryError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Detail  json.RawMessage `json:"detail,omitempty"`
}

func (e *StatusError) Error() string {
	var parts []string
	for _, re := range e.Errors {
		parts = append(parts, strings.TrimSuffix(fmt.Sprintf("%s: %s", re.Code, re.Message), ": "))
	}
	if len(parts) == 0 && e.Body != "" {
		parts = append(parts, e.Body)
	}
	if e.RetryAfter != "" {
		parts = append(parts, fmt.Sprintf("retry after %s", e.RetryAfter))
	}
	if len(parts) == 0 {
		return e.Status
	}
	return fmt.Sprintf("%s: %s", e.Status, strings.Join(parts, "; "))
}

func (e *StatusError) Unwrap() error {
	switch e.StatusCode {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		return ErrDenied
	case http.StatusTooManyRequests:
		return ErrTooManyRequests
	}
	return nil
}

// errorBodyLimit bounds an error body read for its message.
const errorBodyLimit = 4 << 10

func statusError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
	e := &StatusError{StatusCode: resp.StatusCode, Status: resp.Status}
	if resp.StatusCode == http.StatusTooManyRequests {
		e.RetryAfter = resp.Header.Get("Retry-After")
	}
	var body struct {
		Errors []RegistryError `json:"errors"`
	}
	if json.Unmarshal(raw, &body) == nil && len(body.Errors) > 0 {
		e.Errors = body.Errors
		return e
	}
	e.Body = strings.TrimSpace(string(raw))
	return e
}
