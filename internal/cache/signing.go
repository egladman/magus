package cache

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/egladman/magus/internal/json"
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
//
// Members extends the envelope to cover the artifact's NON-manifest files (the build
// log, the output descriptor, the key lines): one content hash per cache-relative
// path. The signature is computed over the manifest bytes CONCATENATED with the
// canonical rendering of that map (signedPayload), so one signature still
// authenticates the whole artifact - the manifest already commits to every cas blob,
// and Members now commits to everything else. An envelope without Members is a
// pre-Members producer: its manifest and blobs verify exactly as before, and the
// unauthenticated extras are dropped on import rather than trusted.
type sigEnvelope struct {
	Alg            string            `json:"alg"`
	KeyID          string            `json:"keyid"`
	ManifestSHA256 string            `json:"manifest_sha256"` // hex; lets a verifier confirm it holds the signed manifest
	Members        map[string]string `json:"members,omitempty"`
	Sig            string            `json:"sig"` // base64(ed25519 signature over signedPayload)
}

// signedPayload renders the exact bytes a signature covers: the manifest, then each
// extra member as "path\x00sha256\n" in sorted path order. Sorting makes the payload
// independent of tar order, and the NUL separator keeps a crafted path from forging a
// different (path, hash) split. Empty members reproduce the pre-Members payload
// byte-for-byte, so an old signature still verifies against this function.
func signedPayload(manifestBytes []byte, members map[string]string) []byte {
	if len(members) == 0 {
		return manifestBytes
	}
	paths := make([]string, 0, len(members))
	for p := range members {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	buf := make([]byte, 0, len(manifestBytes)+len(paths)*80)
	buf = append(buf, manifestBytes...)
	for _, p := range paths {
		buf = append(buf, p...)
		buf = append(buf, 0)
		buf = append(buf, members[p]...)
		buf = append(buf, '\n')
	}
	return buf
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
	pub := s.priv.Public().(ed25519.PublicKey) //nolint:forcetypeassert // always ed25519.PublicKey
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
	pub := priv.Public().(ed25519.PublicKey) //nolint:forcetypeassert // always ed25519.PublicKey
	return &signer{priv: priv, keyid: keyID(pub)}, nil
}

// sign returns a signature.json envelope authenticating manifestBytes plus every
// extra member (path -> content sha256) the artifact ships.
func (s *signer) sign(manifestBytes []byte, members map[string]string) ([]byte, error) {
	sum := sha256.Sum256(manifestBytes)
	env := sigEnvelope{
		Alg:            sigAlg,
		KeyID:          s.keyid,
		ManifestSHA256: hex.EncodeToString(sum[:]),
		Members:        members,
		Sig:            base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, signedPayload(manifestBytes, members))),
	}
	return json.Marshal(env)
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
// Extra artifact members are authenticated by the same call: the caller passes the
// (path -> content sha256) map it actually received, and it must match the signed
// Members map exactly - a tampered, added, or dropped log is a verification failure,
// not a silently-trusted file. It returns the set of member paths the signature
// covers so the caller can commit exactly those.
func (v *verifier) verify(sigBytes, manifestBytes []byte, gotMembers map[string]string) error {
	var env sigEnvelope
	if err := json.Unmarshal(sigBytes, &env); err != nil {
		return fmt.Errorf("signature: parse: %w", err)
	}
	if env.Alg != sigAlg {
		return fmt.Errorf("signature: unsupported alg %q", env.Alg)
	}
	if err := verifyMembers(env.Members, gotMembers); err != nil {
		return err
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
	if !ed25519.Verify(pub, signedPayload(manifestBytes, env.Members), sig) {
		return errors.New("signature: verification failed")
	}
	return nil
}

// verifyMembers reports whether the extra members received match the signed set
// exactly. Both directions matter: an extra file the signature does not cover is
// unauthenticated content smuggled into a trusted artifact, and a missing one means
// the artifact was truncated. A pre-Members envelope (signed empty) is accepted only
// when nothing extra arrived - the caller drops the unauthenticated extras first, so
// an old producer keeps working without its log ever being trusted.
func verifyMembers(want, got map[string]string) error {
	for path, wantSum := range want {
		gotSum, ok := got[path]
		if !ok {
			return fmt.Errorf("signature: artifact is missing signed member %q", path)
		}
		if gotSum != wantSum {
			return fmt.Errorf("signature: member %q content does not match the signature", path)
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			return fmt.Errorf("signature: artifact carries unsigned member %q", path)
		}
	}
	return nil
}
