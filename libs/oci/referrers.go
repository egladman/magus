package oci

import (
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

// ReferrersTag is the fallback tag the distribution spec names for subject's referrers on
// a registry without the Referrers API: `<alg>-<encoded>`, sha256-<hex> for a sha256
// digest.
func ReferrersTag(subject digest.Digest) string {
	alg, encoded := subject.Algorithm().String(), subject.Encoded()
	return fmt.Sprintf("%.32s-%.64s", alg, encoded)
}

// Referrers lists the manifests in subject's repository whose subject is subject.Digest,
// as descriptors carrying each one's artifactType and annotations. A non-empty
// artifactType keeps only those of that type. It asks the Referrers API, and on a 404
// reads the ReferrersTag index instead; no index there is an empty answer.
func (c *Client) Referrers(ctx context.Context, subject Reference, artifactType string) ([]ocispec.Descriptor, error) {
	if subject.Digest == "" {
		return nil, fmt.Errorf("oci: referrers of %s: name the subject by digest", subject)
	}
	if err := subject.Digest.Validate(); err != nil {
		return nil, fmt.Errorf("oci: referrers of %s: %w", subject, err)
	}
	repo := subject.repository()
	idx, filtered, err := c.referrersAPI(ctx, repo, subject.Digest, artifactType)
	if errors.Is(err, ErrNotFound) {
		idx, filtered, err = c.referrersIndex(ctx, repo, subject.Digest)
	}
	if err != nil {
		return nil, fmt.Errorf("oci: referrers of %s: %w", subject, err)
	}
	if artifactType == "" || filtered {
		return idx.Manifests, nil
	}
	var out []ocispec.Descriptor
	for _, d := range idx.Manifests {
		if d.ArtifactType == artifactType {
			out = append(out, d)
		}
	}
	return out, nil
}

// referrersAPI asks GET /v2/<name>/referrers/<digest>. filtered reports that the registry
// applied the artifactType filter itself (OCI-Filters-Applied); a registry may ignore it.
func (c *Client) referrersAPI(ctx context.Context, repo Reference, subject digest.Digest, artifactType string) (idx ocispec.Index, filtered bool, err error) {
	u := c.base(repo) + "/referrers/" + subject.String()
	if artifactType != "" {
		u += "?artifactType=" + url.QueryEscape(artifactType)
	}
	resp, err := c.send(ctx, repo, "pull", func(auth string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		setAuth(req, auth)
		req.Header.Set("Accept", ocispec.MediaTypeImageIndex)
		return req, nil
	})
	if err != nil {
		return idx, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return idx, false, statusError(resp)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, registryReplyLimit))
	if err != nil {
		return idx, false, err
	}
	if err := json.Unmarshal(raw, &idx); err != nil {
		return idx, false, fmt.Errorf("decode the referrers index: %w", err)
	}
	return idx, strings.Contains(resp.Header.Get("OCI-Filters-Applied"), "artifactType"), nil
}

// referrersIndex reads the ReferrersTag index; a tag that does not exist yet is an empty
// index.
func (c *Client) referrersIndex(ctx context.Context, repo Reference, subject digest.Digest) (idx ocispec.Index, filtered bool, err error) {
	tag := ReferrersTag(subject)
	raw, err := c.getManifest(ctx, repo, tag)
	if errors.Is(err, ErrNotFound) {
		return ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex}, false, nil
	}
	if err != nil {
		return idx, false, err
	}
	if raw.MediaType != ocispec.MediaTypeImageIndex {
		return idx, false, fmt.Errorf("the referrers tag %s holds %q, want an image index", tag, raw.MediaType)
	}
	if err := json.Unmarshal(raw.Body, &idx); err != nil {
		return idx, false, fmt.Errorf("decode the referrers tag %s: %w", tag, err)
	}
	return idx, false, nil
}

// addToReferrersTag adds m, confirmed under d, to its subject's ReferrersTag index. A
// manifest with no subject is not a referrer and changes nothing; one the index already
// lists is not added twice.
func (c *Client) addToReferrersTag(ctx context.Context, repo Reference, m RawManifest, d digest.Digest) error {
	var man struct {
		ArtifactType string              `json:"artifactType"`
		Config       ocispec.Descriptor  `json:"config"`
		Subject      *ocispec.Descriptor `json:"subject"`
		Annotations  map[string]string   `json:"annotations"`
	}
	if err := json.Unmarshal(m.Body, &man); err != nil {
		return fmt.Errorf("decode manifest %s: %w", d, err)
	}
	if man.Subject == nil {
		return nil
	}
	idx, _, err := c.referrersIndex(ctx, repo, man.Subject.Digest)
	if err != nil {
		return err
	}
	for _, have := range idx.Manifests {
		if have.Digest == d {
			return nil
		}
	}
	// image-spec: an artifact with no artifactType is typed by its config's media type.
	artifactType := man.ArtifactType
	if artifactType == "" {
		artifactType = man.Config.MediaType
	}
	idx.Manifests = append(idx.Manifests, ocispec.Descriptor{
		MediaType:    m.MediaType,
		Digest:       d,
		Size:         int64(len(m.Body)),
		ArtifactType: artifactType,
		Annotations:  man.Annotations,
	})
	body, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	tag := ReferrersTag(man.Subject.Digest)
	if _, _, err := c.putManifest(ctx, repo, tag, ocispec.MediaTypeImageIndex, body); err != nil {
		return fmt.Errorf("referrers tag %s: %w", tag, err)
	}
	return nil
}
