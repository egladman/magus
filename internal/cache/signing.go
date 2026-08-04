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
	"strconv"
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
	// sigAlg is the pre-domain scheme: a bare ed25519 signature over the manifest
	// bytes alone. Still ACCEPTED (artifacts signed by an older magus keep
	// verifying, minus their unauthenticated extras), never produced.
	sigAlg = "ed25519"
	// sigAlgV2 is what magus produces now: ed25519 over signedPayload - a domain tag,
	// the length-prefixed manifest, and the extra members' digests.
	sigAlgV2 = "ed25519-domain-v2"
	// keyIDLen is the hex length of a derived keyid (first 8 bytes of SHA-256(pubkey)).
	keyIDLen = 16
)

// sigEnvelope is the JSON written to signature.json. keyid is derived from the
// public key (not chosen), so the verifier treats it only as a trust-set lookup hint.
//
// Members extends the envelope to cover the artifact's NON-manifest files (the build
// log, the output descriptor, the key inputs): one content hash per cache-relative
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

// Signing domains. A signature is only meaningful for the KIND of object it was made
// over: without this, a signed output bundle (metadata + captured bytes) could be
// re-tarred as a cache artifact, since the bundle's metadata JSON unmarshals into a
// Manifest as an all-zero value and would then replay as a successful entry. That
// turns a published FAILING run into a teammate's cached "pass". The domain string is
// the first thing hashed, so a signature made in one domain can never verify in the
// other.
const (
	domainArtifact = "magus-cache-artifact-v1\x00"
	domainBundle   = "magus-output-bundle-v1\x00"
)

// signedPayload renders the exact bytes a signature covers: the domain tag, the
// manifest's length and bytes, then each extra member as "path\x00sha256\n" in sorted
// path order. Sorting makes the payload independent of tar order; the NUL separator
// and the explicit manifest length keep the (manifest, members) split injective, so a
// crafted member path cannot be re-read as trailing manifest bytes.
func signedPayload(domain string, manifestBytes []byte, members map[string]string) []byte {
	paths := make([]string, 0, len(members))
	for p := range members {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	buf := make([]byte, 0, len(domain)+24+len(manifestBytes)+len(paths)*80)
	buf = append(buf, domain...)
	buf = strconv.AppendInt(buf, int64(len(manifestBytes)), 10)
	buf = append(buf, '\n')
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
// extra member (path -> content sha256) the object ships, within domain. The alg
// records the SCHEME, not just the curve: an older magus reading a v2 envelope says
// "unsupported alg" instead of the misleading "verification failed" it would report
// for a payload shape it cannot construct.
func (s *signer) sign(domain string, manifestBytes []byte, members map[string]string) ([]byte, error) {
	sum := sha256.Sum256(manifestBytes)
	env := sigEnvelope{
		Alg:            sigAlgV2,
		KeyID:          s.keyid,
		ManifestSHA256: hex.EncodeToString(sum[:]),
		Members:        members,
		Sig:            base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, signedPayload(domain, manifestBytes, members))),
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
// Extra members are authenticated by the same call: the caller passes the (path ->
// content sha256) map it actually received, and it must match the signed Members map
// exactly - a tampered, added, or dropped log is a verification failure, not a
// silently-trusted file. domain separates object KINDS, so a signature over an output
// bundle can never verify as a cache artifact.
//
// It returns legacy=true for a pre-domain envelope, whose signature covers ONLY the
// manifest: the caller must then discard every extra member it received, because
// nothing authenticates them.
func (v *verifier) verify(domain string, sigBytes, manifestBytes []byte, gotMembers map[string]string) (legacy bool, err error) {
	var env sigEnvelope
	if err := json.Unmarshal(sigBytes, &env); err != nil {
		return false, fmt.Errorf("signature: parse: %w", err)
	}
	switch env.Alg {
	case sigAlgV2:
		// The signed set must match exactly what arrived.
		if err := verifyMembers(env.Members, gotMembers); err != nil {
			return false, err
		}
	case sigAlg:
		// Pre-domain producer: it signed the manifest alone, so any extras that came
		// with it are unauthenticated and the caller drops them. Rejecting the
		// artifact instead would turn every release-built entry into a miss.
		legacy = true
		env.Members = nil
	default:
		return false, fmt.Errorf("signature: unsupported alg %q", env.Alg)
	}
	// Diagnostic pre-check, not a trust factor: the Ed25519 verify below already
	// binds the signature to manifestBytes. This just yields a clearer error when
	// the shipped manifest isn't the one the envelope names.
	sum := sha256.Sum256(manifestBytes)
	if env.ManifestSHA256 != hex.EncodeToString(sum[:]) {
		return false, errors.New("signature: manifest digest mismatch")
	}
	pub, ok := v.keys[env.KeyID]
	if !ok {
		return false, fmt.Errorf("signature: keyid %q not in trust set", env.KeyID)
	}
	sig, err := base64.StdEncoding.DecodeString(env.Sig)
	if err != nil {
		return false, fmt.Errorf("signature: decode: %w", err)
	}
	// A legacy envelope's signature is over the bare manifest, exactly as the older
	// magus produced it; a v2 one is over the domain-tagged payload.
	payload := signedPayload(domain, manifestBytes, env.Members)
	if legacy {
		payload = manifestBytes
	}
	if !ed25519.Verify(pub, payload, sig) {
		return false, errors.New("signature: verification failed")
	}
	return legacy, nil
}

// verifyMembers reports whether the extra members received match the signed set
// exactly. Both directions matter: an extra file the signature does not cover is
// unauthenticated content smuggled into a trusted artifact, and a missing one means
// the artifact was truncated.
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
