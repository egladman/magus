package cache

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/codec"
)

// Remote cache artifacts are authenticated with Ed25519 signatures. The trust is
// asymmetric: the private seed is a secret held only by trusted CI; the public
// keys are committed in the magusfile, so a holder of only the public keys can
// verify (replay) artifacts but never forge one.
//
// What is signed is the manifest's exact on-disk bytes, which travel verbatim in
// the artifact tar — so there is no canonicalization to get wrong. The manifest
// commits to the cache key and every output blob's content hash, and importArtifact's
// content-address checks bind the shipped blobs back to it, so one signature over
// the manifest authenticates the whole artifact.

const (
	// sigFileName is the artifact-tar member holding the detached signature envelope.
	sigFileName = "signature.json"
	// sigAlg is the only signature algorithm magus produces or accepts.
	sigAlg = "ed25519"
	// keyIDLen is the hex length of a derived keyid (first 8 bytes of SHA-256(pubkey)).
	keyIDLen = 16
)

// sigEnvelope is the JSON written to signature.json. keyid is derived from the
// public key (not chosen), so the verifier treats it only as a trust-set lookup hint.
type sigEnvelope struct {
	Alg            string `json:"alg"`
	KeyID          string `json:"keyid"`
	ManifestSHA256 string `json:"manifest_sha256"` // hex; lets a verifier confirm it holds the signed manifest
	Sig            string `json:"sig"`             // base64(ed25519 signature over the manifest bytes)
}

// KeyMaterial is a freshly minted signing keypair, base64-encoded. SeedB64 is the
// secret (MAGUS_CACHE_SIGNING_KEY); PubB64 goes in trusted_keys.
type KeyMaterial struct {
	SeedB64 string
	PubB64  string
	KeyID   string
}

// KeyInfo is the public identity of a key: its base64 public key and derived keyid.
type KeyInfo struct {
	PubB64 string
	KeyID  string
}

// GenerateSigningKey mints a fresh Ed25519 keypair. Lives here, beside the
// verifier, so the keyid derivation never drifts.
func GenerateSigningKey() (KeyMaterial, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyMaterial{}, fmt.Errorf("magus/cache: generate signing key: %w", err)
	}
	return KeyMaterial{
		SeedB64: base64.StdEncoding.EncodeToString(priv.Seed()),
		PubB64:  base64.StdEncoding.EncodeToString(pub),
		KeyID:   keyID(pub),
	}, nil
}

// TrustedKeyInfo validates a base64 Ed25519 public key and returns it normalized
// with its derived keyid — for `magus config cache key id <pubkey>`.
func TrustedKeyInfo(pubB64 string) (KeyInfo, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil {
		return KeyInfo{}, fmt.Errorf("magus/cache: trusted key: not valid base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return KeyInfo{}, fmt.Errorf("magus/cache: trusted key: expected %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	pub := ed25519.PublicKey(raw)
	return KeyInfo{PubB64: base64.StdEncoding.EncodeToString(pub), KeyID: keyID(pub)}, nil
}

// SigningKeyInfo derives the public key + keyid of a base64 seed without echoing
// the seed — for `magus config cache key id` reading MAGUS_CACHE_SIGNING_KEY.
func SigningKeyInfo(seedB64 string) (KeyInfo, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seedB64))
	if err != nil {
		return KeyInfo{}, fmt.Errorf("magus/cache: signing key: not valid base64: %w", err)
	}
	s, err := newSigner(raw)
	if err != nil {
		return KeyInfo{}, err
	}
	pub := s.priv.Public().(ed25519.PublicKey)
	return KeyInfo{PubB64: base64.StdEncoding.EncodeToString(pub), KeyID: s.keyid}, nil
}

// keyID is the first 8 bytes of SHA-256(pubkey), hex-encoded: a pure function of
// the key, so there is no human-chosen label to mistype or fall out of sync.
func keyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])[:keyIDLen]
}

// signer produces signature envelopes for manifest bytes. A nil signer means the
// machine holds no key and cannot sign — and so cannot publish trusted artifacts.
type signer struct {
	priv  ed25519.PrivateKey
	keyid string
}

// newSigner builds a signer from a 32-byte Ed25519 seed.
func newSigner(seed []byte) (*signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("magus/cache: signing key must be %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return &signer{priv: priv, keyid: keyID(priv.Public().(ed25519.PublicKey))}, nil
}

// sign returns a signature.json envelope authenticating manifestBytes.
func (s *signer) sign(manifestBytes []byte) ([]byte, error) {
	sum := sha256.Sum256(manifestBytes)
	env := sigEnvelope{
		Alg:            sigAlg,
		KeyID:          s.keyid,
		ManifestSHA256: hex.EncodeToString(sum[:]),
		Sig:            base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, manifestBytes)),
	}
	return codec.Marshal(env)
}

// verifier authenticates artifacts against trusted public keys, indexed by derived
// keyid. A nil verifier means no verification (local-only cache); the remote path
// requires one, enforced where the backend is wired.
type verifier struct {
	keys map[string]ed25519.PublicKey
}

// newVerifier builds a verifier from raw 32-byte Ed25519 public keys. An empty set
// errors: a verifier that trusts nothing is a misconfiguration, not an allow-all.
func newVerifier(pubkeys [][]byte) (*verifier, error) {
	if len(pubkeys) == 0 {
		return nil, errors.New("magus/cache: trust set is empty")
	}
	v := &verifier{keys: make(map[string]ed25519.PublicKey, len(pubkeys))}
	for i, pk := range pubkeys {
		if len(pk) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("magus/cache: trusted key %d must be %d bytes, got %d", i, ed25519.PublicKeySize, len(pk))
		}
		key := ed25519.PublicKey(append([]byte(nil), pk...))
		v.keys[keyID(key)] = key
	}
	return v, nil
}

// verify reports whether sigBytes (a signature.json) authenticates manifestBytes
// against the trust set. It returns nil only when the envelope is well-formed, its
// keyid resolves to a trusted key, the envelope commits to this exact manifest,
// and the Ed25519 signature verifies. Every other path is an error, so a caller
// that treats any error as "reject and fall back to a local build" fails closed.
func (v *verifier) verify(sigBytes, manifestBytes []byte) error {
	var env sigEnvelope
	if err := codec.Unmarshal(sigBytes, &env); err != nil {
		return fmt.Errorf("signature: parse: %w", err)
	}
	if env.Alg != sigAlg {
		return fmt.Errorf("signature: unsupported alg %q", env.Alg)
	}
	// Diagnostic pre-check, not a trust factor: the Ed25519 verify below already
	// binds the signature to manifestBytes. This just yields a clearer error when
	// the shipped manifest isn't the one the envelope names.
	sum := sha256.Sum256(manifestBytes)
	if env.ManifestSHA256 != hex.EncodeToString(sum[:]) {
		return errors.New("signature: manifest digest mismatch")
	}
	pub, ok := v.keys[env.KeyID]
	if !ok {
		return fmt.Errorf("signature: keyid %q not in trust set", env.KeyID)
	}
	sig, err := base64.StdEncoding.DecodeString(env.Sig)
	if err != nil {
		return fmt.Errorf("signature: decode: %w", err)
	}
	if !ed25519.Verify(pub, manifestBytes, sig) {
		return errors.New("signature: verification failed")
	}
	return nil
}
