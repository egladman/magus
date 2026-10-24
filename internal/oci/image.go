package oci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	mediaTypeDockerManifest     = "application/vnd.docker.distribution.manifest.v2+json"
	mediaTypeDockerManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
)

// manifestMediaTypes are the manifest and index types Manifest accepts and PutManifest
// may send.
var manifestMediaTypes = []string{
	ocispec.MediaTypeImageManifest,
	ocispec.MediaTypeImageIndex,
	mediaTypeDockerManifest,
	mediaTypeDockerManifestList,
}

// RawManifest is a manifest or index exactly as a registry served it. Descriptor carries
// the media type, the digest the bytes hash to, and their length.
type RawManifest struct {
	ocispec.Descriptor
	Body []byte
}

// Manifest fetches the manifest or index ref pins. ref must carry a digest: the body is
// verified against it before it is returned, and a tag could name a different one
// tomorrow. The media type is the Content-Type the registry sent, which must agree with
// the body's own mediaType field when it has one.
func (c *Client) Manifest(ctx context.Context, ref Reference) (RawManifest, error) {
	if ref.Digest == "" {
		return RawManifest{}, fmt.Errorf("oci: fetch %s: name a digest, a tag can move", ref)
	}
	// Validated before FromBytes, which panics on an algorithm this build cannot hash.
	if err := ref.Digest.Validate(); err != nil {
		return RawManifest{}, fmt.Errorf("oci: %s: %w", ref, err)
	}
	resp, err := c.send(ctx, ref, "pull", func(auth string) (*http.Request, error) {
		req, err := c.newGetRequest(ctx, ref, auth, "/manifests/"+ref.Digest.String())
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", strings.Join(manifestMediaTypes, ", "))
		return req, nil
	})
	if err != nil {
		return RawManifest{}, fmt.Errorf("fetch %s: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return RawManifest{}, fmt.Errorf("oci: fetch %s: %s", ref, statusLine(resp))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, registryReplyLimit+1))
	if err != nil {
		return RawManifest{}, fmt.Errorf("oci: fetch %s: %w", ref, err)
	}
	if len(body) > registryReplyLimit {
		return RawManifest{}, fmt.Errorf("oci: fetch %s: the manifest is over %d bytes", ref, registryReplyLimit)
	}
	if got := ref.Digest.Algorithm().FromBytes(body); got != ref.Digest {
		return RawManifest{}, fmt.Errorf("%s served %s: %w", ref, got, ErrManifestDigest)
	}
	mediaType, err := manifestMediaType(resp.Header.Get("Content-Type"), body)
	if err != nil {
		return RawManifest{}, fmt.Errorf("fetch %s: %w", ref, err)
	}
	return RawManifest{
		Descriptor: ocispec.Descriptor{MediaType: mediaType, Digest: ref.Digest, Size: int64(len(body))},
		Body:       body,
	}, nil
}

func manifestMediaType(contentType string, body []byte) (string, error) {
	var header string
	if contentType != "" {
		parsed, _, err := mime.ParseMediaType(contentType)
		if err != nil {
			return "", fmt.Errorf("unusable Content-Type %q: %w", contentType, err)
		}
		header = parsed
	}
	var inBody struct {
		MediaType string `json:"mediaType"`
	}
	if err := json.Unmarshal(body, &inBody); err != nil {
		return "", fmt.Errorf("decode manifest: %w", err)
	}
	switch {
	case header != "" && inBody.MediaType != "" && header != inBody.MediaType:
		return "", fmt.Errorf("Content-Type %s disagrees with the manifest's mediaType %s", header, inBody.MediaType)
	case header == "":
		header = inBody.MediaType
	}
	if !slices.Contains(manifestMediaTypes, header) {
		return "", fmt.Errorf("media type %q is not a manifest or index", header)
	}
	return header, nil
}

// FetchBlob streams the blob d names from repo ("<registry>/<repository>") into w. It
// reads at most d.Size bytes and fails if the stream is shorter, longer, or does not hash
// to d.Digest. w may already hold a prefix of the blob when it fails, so a caller writes
// to a place it can discard.
func (c *Client) FetchBlob(ctx context.Context, repo string, d ocispec.Descriptor, w io.Writer) error {
	ref, err := ParseRepository(repo)
	if err != nil {
		return err
	}
	vr, err := newVerifyReader(nil, d)
	if err != nil {
		return fmt.Errorf("fetch %s blob: %w", ref, err)
	}
	resp, err := c.send(ctx, ref, "pull", func(auth string) (*http.Request, error) {
		return c.newGetRequest(ctx, ref, auth, "/blobs/"+d.Digest.String())
	})
	if err != nil {
		return fmt.Errorf("fetch %s blob: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oci: fetch %s blob: %s", ref, statusLine(resp))
	}
	vr.r = resp.Body
	if _, err := io.Copy(w, vr); err != nil {
		return fmt.Errorf("oci: fetch %s blob %s: %w", ref, d.Digest, err)
	}
	return nil
}

// BlobExists reports whether repo ("<registry>/<repository>") holds the blob d. Any
// answer but 200 and 404 is an error rather than "absent".
func (c *Client) BlobExists(ctx context.Context, repo string, d digest.Digest) (bool, error) {
	ref, err := ParseRepository(repo)
	if err != nil {
		return false, err
	}
	if err := d.Validate(); err != nil {
		return false, fmt.Errorf("oci: probe %s: %w", ref, err)
	}
	return c.blobExists(ctx, ref, "pull", d)
}

// PushBlob uploads the blob d names, read from r, to repo ("<registry>/<repository>"),
// and does nothing when the registry already holds it. r is read only when the upload
// runs, and its end must be d.Size bytes hashing to d.Digest: otherwise the request is
// aborted, so the registry never receives a complete blob that is not the one named.
func (c *Client) PushBlob(ctx context.Context, repo string, d ocispec.Descriptor, r io.Reader) error {
	ref, err := ParseRepository(repo)
	if err != nil {
		return err
	}
	return c.pushBlob(ctx, ref, d, r)
}

// pushBlob is the two-step upload the v2 API defines: ask for a session, then PUT the
// bytes with the digest. A blob the registry already holds is skipped, which is what makes
// re-publishing an unchanged artifact nearly free.
func (c *Client) pushBlob(ctx context.Context, ref Reference, d ocispec.Descriptor, r io.Reader) error {
	vr, err := newVerifyReader(r, d)
	if err != nil {
		return fmt.Errorf("upload to %s: %w", ref.Repository, err)
	}
	if n, ok := sourceLen(r); ok && n != d.Size {
		return fmt.Errorf("oci: upload to %s: the source is %d bytes, %d declared", ref.Repository, n, d.Size)
	}
	have, err := c.blobExists(ctx, ref, "push,pull", d.Digest)
	if err != nil {
		return err
	}
	if have {
		return nil
	}
	upload, err := c.startUpload(ctx, ref, d)
	if err != nil {
		return err
	}
	return c.sendBlob(ctx, ref, upload, d, vr)
}

// sourceLen reports how many unread bytes r holds, for the readers that know
// (bytes.Reader, bytes.Buffer, strings.Reader).
func sourceLen(r io.Reader) (int64, bool) {
	l, ok := r.(interface{ Len() int })
	if !ok {
		return 0, false
	}
	return int64(l.Len()), true
}

// startUpload opens an upload session and returns the URL to PUT the blob to, with the
// digest query set.
func (c *Client) startUpload(ctx context.Context, ref Reference, d ocispec.Descriptor) (*url.URL, error) {
	resp, err := c.send(ctx, ref, "push,pull", func(auth string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base(ref)+"/blobs/uploads/", nil)
		if err != nil {
			return nil, err
		}
		setAuth(req, auth)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("start upload to %s: %w", ref.Repository, err)
	}
	// Closed AFTER the status check, not before: statusLine reads the body for whatever
	// the registry said, and closing first discards the explanation of the very first
	// error a misconfigured push hits.
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("oci: start upload to %s: %s", ref.Repository, statusLine(resp))
	}
	location := resp.Header.Get("Location")
	if location == "" {
		return nil, fmt.Errorf("oci: start upload to %s: the registry returned no upload location", ref.Repository)
	}

	// The location may be absolute or registry-relative, and it may already carry a query.
	upload, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("oci: upload location %q: %w", location, err)
	}
	if !upload.IsAbs() {
		upload.Scheme, upload.Host = "https", ref.Registry
	}
	if upload.Scheme != "https" {
		return nil, fmt.Errorf("oci: upload to %s: the registry named a %s upload location, which would send the blob unprotected", ref.Repository, upload.Scheme)
	}
	q := upload.Query()
	q.Set("digest", d.Digest.String())
	upload.RawQuery = q.Encode()
	return upload, nil
}

// sendBlob sends the verified bytes to the upload URL startUpload returned.
func (c *Client) sendBlob(ctx context.Context, ref Reference, upload *url.URL, d ocispec.Descriptor, vr *verifyReader) error {
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, upload.String(), vr)
	if err != nil {
		return err
	}
	// Credentials go only to the registry itself; another host named by Location is
	// holding the upload on its behalf and gets a signed URL, not a login.
	if upload.Host == ref.Registry {
		auth, err := c.authorize(ctx, ref, "push,pull")
		if err != nil {
			return err
		}
		setAuth(put, auth)
	}
	put.Header.Set("Content-Type", "application/octet-stream")
	put.ContentLength = d.Size
	done, err := c.do(put)
	if err != nil {
		return fmt.Errorf("upload %s: %w", d.Digest, err)
	}
	defer done.Body.Close()
	if done.StatusCode != http.StatusCreated {
		return fmt.Errorf("oci: upload %s: %s", d.Digest, statusLine(done))
	}
	// The transport sends only ContentLength bytes, so a source with more has not been
	// read to its end yet. The registry holds the declared prefix, which hashes to d.
	if _, err := vr.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("upload %s: %w", d.Digest, err)
	}
	return nil
}

// PutManifest pushes body to repo ("<registry>/<repository>") by its digest, then under
// each of tags, and returns the digest the registry confirmed. A registry that sends a
// Docker-Content-Digest naming anything but the body's digest is an error. With no tags the
// manifest is addressable by digest only.
func (c *Client) PutManifest(ctx context.Context, repo, mediaType string, body []byte, tags ...string) (digest.Digest, error) {
	ref, err := ParseRepository(repo)
	if err != nil {
		return "", err
	}
	if !slices.Contains(manifestMediaTypes, mediaType) {
		return "", fmt.Errorf("oci: push to %s: media type %q is not a manifest or index", ref, mediaType)
	}
	for _, t := range tags {
		if err := ValidateTag(t); err != nil {
			return "", fmt.Errorf("push to %s: %w", ref, err)
		}
	}
	confirmed, err := c.putManifest(ctx, ref, digest.FromBytes(body).String(), mediaType, body)
	if err != nil {
		return "", err
	}
	for i, t := range tags {
		if slices.Contains(tags[:i], t) {
			continue
		}
		got, err := c.putManifest(ctx, ref, t, mediaType, body)
		if err != nil {
			return "", err
		}
		if got != confirmed {
			return "", fmt.Errorf("oci: tag %s:%s: the registry confirmed %s after confirming %s", ref, t, got, confirmed)
		}
	}
	return confirmed, nil
}

// verifyReader passes r through while hashing it, and turns the end of the stream into
// an error unless exactly want bytes arrived and they hash to the digest.
type verifyReader struct {
	r        io.Reader
	verifier digest.Verifier
	want     int64
	read     int64
}

func newVerifyReader(r io.Reader, d ocispec.Descriptor) (*verifyReader, error) {
	// Validated before Verifier, which panics on an algorithm this build cannot hash.
	if err := d.Digest.Validate(); err != nil {
		return nil, fmt.Errorf("unusable digest %q: %w", d.Digest, err)
	}
	if d.Size < 0 {
		return nil, fmt.Errorf("negative size %d for %s", d.Size, d.Digest)
	}
	return &verifyReader{r: r, verifier: d.Digest.Verifier(), want: d.Size}, nil
}

func (v *verifyReader) Read(p []byte) (int, error) {
	// One byte past the declared size, so an overlong stream is caught here rather than
	// read to its end.
	n, err := io.LimitReader(v.r, v.want+1-v.read).Read(p)
	v.read += int64(n)
	_, _ = v.verifier.Write(p[:n])
	if v.read > v.want {
		return n, fmt.Errorf("the blob is longer than the %d bytes declared", v.want)
	}
	if errors.Is(err, io.EOF) {
		if v.read != v.want {
			return n, fmt.Errorf("the blob is %d bytes, %d declared", v.read, v.want)
		}
		if !v.verifier.Verified() {
			return n, ErrBlobDigest
		}
	}
	return n, err
}
