// Package oci publishes and fetches a single blob as an OCI artifact, over the registry
// v2 HTTP API.
//
// It exists so a workspace can hand its knowledge graph to everyone else who works on the
// repository without anyone rebuilding it. A registry is the right store for that: it is
// already content-addressed, it already has a public read path, and a team that can clone
// the repository can already reach its packages.
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

// Reference names one artifact: a registry host, a repository path, and a tag.
type Reference struct {
	Registry   string // e.g. "ghcr.io"
	Repository string // e.g. "egladman/magus/knowledge-graph"
	Tag        string // e.g. "latest"
}

// ParseReference reads "ghcr.io/owner/name/artifact:tag". The tag is required rather than
// defaulted to "latest": a publish that silently retagged latest because someone omitted
// a tag is the kind of mistake a registry cannot undo.
//
// The tag separator is the last colon AFTER the last path separator, because a registry
// may carry a port. Cutting on the first colon reads localhost:5000/team/graph:v1 as host
// "localhost" with the rest as a tag, so a ported registry cannot be addressed at all.
func ParseReference(s string) (Reference, error) {
	colon := strings.LastIndex(s, ":")
	if colon < 0 || colon < strings.LastIndex(s, "/") || colon == len(s)-1 {
		return Reference{}, fmt.Errorf("oci: %q names no tag; write <registry>/<repository>:<tag>", s)
	}
	name, tag := s[:colon], s[colon+1:]
	host, repo, ok := strings.Cut(name, "/")
	if !ok || host == "" || repo == "" || !isRegistryHost(host) {
		return Reference{}, fmt.Errorf("oci: %q names no registry host; write <registry>/<repository>:<tag>", s)
	}
	return Reference{Registry: host, Repository: repo, Tag: tag}, nil
}

// isRegistryHost distinguishes a registry from the first segment of a bare repository
// path, the way a container runtime does: a dot, a port, or the literal "localhost".
// Without it "egladman/magus:v1" would read as a host named "egladman".
func isRegistryHost(host string) bool {
	return strings.Contains(host, ".") || strings.Contains(host, ":") || host == "localhost"
}

func (r Reference) String() string {
	return r.Registry + "/" + r.Repository + ":" + r.Tag
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

// Pull fetches the artifact's single layer and returns its bytes.
//
// wantArtifactType, when non-empty, is checked against the manifest before the layer is
// read: a tag someone else's tooling pushed to is the ordinary way to get bytes that
// parse as JSON and mean something entirely different, and the artifact type is the only
// field that says what the blob was meant to be.
func (c *Client) Pull(ctx context.Context, ref Reference, wantArtifactType string) ([]byte, error) {
	tok, err := c.token(ctx, ref, "pull")
	if err != nil {
		return nil, err
	}
	m, err := c.manifest(ctx, ref, tok)
	if err != nil {
		return nil, err
	}
	if wantArtifactType != "" && m.ArtifactType != wantArtifactType {
		return nil, fmt.Errorf("oci: %s carries artifactType %q, wanted %q", ref, m.ArtifactType, wantArtifactType)
	}
	if len(m.Layers) != 1 {
		return nil, fmt.Errorf("oci: %s has %d layers, wanted exactly 1", ref, len(m.Layers))
	}
	blob, err := c.blob(ctx, ref, tok, m.Layers[0])
	if err != nil {
		return nil, err
	}
	want := m.Layers[0].Digest
	// The digest is the registry's contract, so verifying it is what makes a pull over a
	// plain HTTP hop trustworthy without signing anything. Verifier reports a mismatch
	// rather than panicking on a malformed digest the manifest supplied.
	verifier := want.Verifier()
	if _, err := verifier.Write(blob); err != nil {
		return nil, fmt.Errorf("oci: verify %s layer: %w", ref, err)
	}
	if !verifier.Verified() {
		return nil, fmt.Errorf("oci: %s layer does not match the digest its manifest names (%s)", ref, want)
	}
	return blob, nil
}

func (c *Client) manifest(ctx context.Context, ref Reference, tok string) (ocispec.Manifest, error) {
	req, err := c.get(ctx, ref, tok, "/manifests/"+ref.Tag)
	if err != nil {
		return ocispec.Manifest{}, err
	}
	req.Header.Set("Accept", ocispec.MediaTypeImageManifest)
	resp, err := c.http().Do(req)
	if err != nil {
		return ocispec.Manifest{}, fmt.Errorf("oci: fetch %s: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ocispec.Manifest{}, fmt.Errorf("oci: fetch %s: %s", ref, statusLine(resp))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, registryReplyLimit))
	if err != nil {
		return ocispec.Manifest{}, fmt.Errorf("oci: fetch %s: %w", ref, err)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return ocispec.Manifest{}, fmt.Errorf("oci: decode %s manifest: %w", ref, err)
	}
	return m, nil
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

func (c *Client) get(ctx context.Context, ref Reference, tok, path string) (*http.Request, error) {
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

// Push uploads payload as the artifact's only layer and tags it.
//
// It is NOT idempotent in the way a content-addressed store is: pushing to a tag that
// already exists moves the tag. That is the intent for a floating tag like "latest" and
// is why the caller, not this package, decides which tags to write.
func (c *Client) Push(ctx context.Context, ref Reference, payload []byte, artifactType, layerMediaType string) error {
	tok, err := c.token(ctx, ref, "push,pull")
	if err != nil {
		return err
	}
	layer := ocispec.Descriptor{
		MediaType: layerMediaType,
		Digest:    digest.FromBytes(payload),
		Size:      int64(len(payload)),
	}
	// The empty config blob must exist before a manifest may reference it, even though
	// every artifact in every repository shares the same two bytes.
	if err := c.putBlob(ctx, ref, tok, emptyConfigPayload); err != nil {
		return err
	}
	if err := c.putBlob(ctx, ref, tok, payload); err != nil {
		return err
	}
	m := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: artifactType,
		Config:       ocispec.DescriptorEmptyJSON,
		Layers:       []ocispec.Descriptor{layer},
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

func (c *Client) putManifest(ctx context.Context, ref Reference, tok string, m ocispec.Manifest) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base(ref)+"/manifests/"+ref.Tag, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", ocispec.MediaTypeImageManifest)
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("oci: tag %s: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("oci: tag %s: %s", ref, statusLine(resp))
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
