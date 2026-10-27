package image

import (
	"fmt"

	digest "github.com/opencontainers/go-digest"
)

// Digest is an OCI content digest (algorithm:encoded). Only sha256 is accepted:
// that is what the layout store and registry transport pin. Zero means unset.
type Digest struct {
	d digest.Digest
}

// DiffID is the sha256 of an uncompressed layer tar (config.rootfs.diff_ids).
// It is not the compressed blob digest on the layer Descriptor; that is Digest.
type DiffID struct {
	d Digest
}

// ParseDigest accepts "sha256:<hex>" and rejects tags, bare hex, and other algorithms.
func ParseDigest(s string) (Digest, error) {
	if s == "" {
		return Digest{}, fmt.Errorf("image.digest: empty")
	}
	d, err := digest.Parse(s)
	if err != nil {
		return Digest{}, fmt.Errorf("image.digest: %w", err)
	}
	return WrapDigest(d)
}

// WrapDigest wraps an OCI digest after checking it is sha256.
func WrapDigest(d digest.Digest) (Digest, error) {
	if d == "" {
		return Digest{}, nil
	}
	if err := d.Validate(); err != nil {
		return Digest{}, fmt.Errorf("image.digest: %w", err)
	}
	if d.Algorithm() != digest.SHA256 {
		return Digest{}, fmt.Errorf("image.digest: algorithm %q, want sha256", d.Algorithm())
	}
	return Digest{d: d}, nil
}

// MustDigest is WrapDigest for values already known to be valid sha256 digests
// (descriptors produced inside this package). Panics on a bad digest.
func MustDigest(d digest.Digest) Digest {
	out, err := WrapDigest(d)
	if err != nil {
		panic(err)
	}
	return out
}

func (d Digest) IsZero() bool { return d.d == "" }

func (d Digest) String() string {
	if d.IsZero() {
		return ""
	}
	return d.d.String()
}

func (d Digest) Algorithm() string {
	if d.IsZero() {
		return ""
	}
	return d.d.Algorithm().String()
}

func (d Digest) Encoded() string {
	if d.IsZero() {
		return ""
	}
	return d.d.Encoded()
}

// OCI returns the underlying go-digest value for registry and layout calls.
func (d Digest) OCI() digest.Digest { return d.d }

// ParseDiffID accepts a sha256 digest of an uncompressed layer tar.
func ParseDiffID(s string) (DiffID, error) {
	d, err := ParseDigest(s)
	if err != nil {
		return DiffID{}, fmt.Errorf("image.diffid: %w", err)
	}
	return DiffID{d: d}, nil
}

// WrapDiffID wraps a validated sha256 digest as a DiffID.
func WrapDiffID(d digest.Digest) (DiffID, error) {
	out, err := WrapDigest(d)
	if err != nil {
		return DiffID{}, fmt.Errorf("image.diffid: %w", err)
	}
	return DiffID{d: out}, nil
}

func MustDiffID(d digest.Digest) DiffID {
	out, err := WrapDiffID(d)
	if err != nil {
		panic(err)
	}
	return out
}

func (d DiffID) IsZero() bool       { return d.d.IsZero() }
func (d DiffID) String() string     { return d.d.String() }
func (d DiffID) Algorithm() string  { return d.d.Algorithm() }
func (d DiffID) Encoded() string    { return d.d.Encoded() }
func (d DiffID) Digest() Digest     { return d.d }
func (d DiffID) OCI() digest.Digest { return d.d.OCI() }
