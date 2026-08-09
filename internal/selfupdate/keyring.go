package selfupdate

import (
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"

	json "github.com/egladman/magus/internal/json"
)

//go:embed release-keys.json
var embeddedKeyring []byte

// ReleaseKeys is the keyring every magus binary trusts: the Ed25519 public keys a
// release signature may come from. Signing uses exactly one of them; verification
// accepts any.
//
// A ring rather than a key, because the rotation CONTRIBUTING used to describe was a
// chain - ship a compatibility release signed by the old key that embeds the new one -
// and SelectRelease takes the newest release by default. Anyone who skipped that one
// release jumped to a signature their binary had never been told to trust, and had no
// in-band way back. A standby key ships long before it signs anything, so by the time
// it is used the binaries in the field already trust it and there is no hop to skip.
var ReleaseKeys Keyring

// KeyState is what a key in the ring may do.
type KeyState string

const (
	// KeyActive is the one key releases are currently signed with.
	KeyActive KeyState = "active"
	// KeyStandby is announced and trusted but has never signed. It exists so a
	// rotation has somewhere to go that the field already accepts.
	KeyStandby KeyState = "standby"
	// KeyRetired still verifies artifacts it signed and will sign no more. Drop the
	// entry entirely once nothing you must verify was signed by it.
	KeyRetired KeyState = "retired"
	// KeyRevoked must not verify anything. The state is local bookkeeping: it is what
	// release-index publishes as the signed index's revoked[], which is the only form
	// of the fact a binary in the field can learn.
	KeyRevoked KeyState = "revoked"
)

// Key is one entry in the release keyring.
type Key struct {
	// ID is the fingerprint, derived from Pub rather than recorded, so the file
	// cannot name a key it does not hold.
	ID    string
	State KeyState
	Pub   ed25519.PublicKey
}

// Keyring is the ordered set of release keys, active first.
type Keyring []Key

// KeyID is the fingerprint of a public key: the first 16 hex digits of its SHA-256.
// Short enough to read out in an error, long enough that colliding with it is not a
// thing an attacker can arrange.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])[:16]
}

// keyringFile is the on-disk shape of release-keys.json. Each entry records the state
// and the key; the fingerprint is derived.
type keyringFile struct {
	Keys []struct {
		State KeyState `json:"state"`
		Key   string   `json:"key"`
	} `json:"keys"`
}

func init() {
	ring, err := parseKeyring(embeddedKeyring)
	if err != nil {
		panic("selfupdate: embedded release keyring: " + err.Error())
	}
	if _, err := ring.Active(); err != nil {
		panic("selfupdate: embedded release keyring: " + err.Error())
	}
	ReleaseKeys = ring
}

// parseKeyring reads the keyring file format, rejecting anything that would make a
// verification decision ambiguous later.
func parseKeyring(data []byte) (Keyring, error) {
	var f keyringFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(f.Keys) == 0 {
		return nil, fmt.Errorf("no keys")
	}
	var ring Keyring
	seen := map[string]bool{}
	for i, e := range f.Keys {
		raw, err := hex.DecodeString(e.Key)
		if err != nil {
			return nil, fmt.Errorf("key %d: decode: %w", i, err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("key %d: %d bytes, want %d", i, len(raw), ed25519.PublicKeySize)
		}
		switch e.State {
		case KeyActive, KeyStandby, KeyRetired, KeyRevoked:
		default:
			return nil, fmt.Errorf("key %d: unknown state %q", i, e.State)
		}
		pub := ed25519.PublicKey(raw)
		id := KeyID(pub)
		if seen[id] {
			return nil, fmt.Errorf("key %d: %s appears twice", i, id)
		}
		seen[id] = true
		ring = append(ring, Key{ID: id, State: e.State, Pub: pub})
	}
	return ring, nil
}

// Active returns the single signing key. Two active keys, or none, is a
// configuration error rather than something to resolve by picking one: which key
// signed a release has to be answerable from the file alone.
func (k Keyring) Active() (Key, error) {
	var found Key
	n := 0
	for _, key := range k {
		if key.State == KeyActive {
			found, n = key, n+1
		}
	}
	switch n {
	case 1:
		return found, nil
	case 0:
		return Key{}, fmt.Errorf("no active key in the release keyring")
	default:
		return Key{}, fmt.Errorf("%d active keys in the release keyring, want exactly 1", n)
	}
}

// Verifiers returns the keys a signature may come from: every state except revoked.
func (k Keyring) Verifiers() Keyring {
	out := make(Keyring, 0, len(k))
	for _, key := range k {
		if key.State != KeyRevoked {
			out = append(out, key)
		}
	}
	return out
}

// RevokedIDs lists the fingerprints release-index publishes as the index's revoked[].
func (k Keyring) RevokedIDs() []string {
	var out []string
	for _, key := range k {
		if key.State == KeyRevoked {
			out = append(out, key.ID)
		}
	}
	return out
}

// Without returns the ring minus the named fingerprints. Applied to the ring carried
// forward after the signed index has been read, so a key the publisher revoked cannot
// verify anything downloaded afterwards - even though this binary was built trusting it.
func (k Keyring) Without(ids []string) Keyring {
	if len(ids) == 0 {
		return k
	}
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	out := make(Keyring, 0, len(k))
	for _, key := range k {
		if !drop[key.ID] {
			out = append(out, key)
		}
	}
	return out
}

// Verify reports which key in the ring signed msg. Trying the ring rather than
// trusting a signer named in the payload keeps key selection off untrusted input: the
// declared key_id is then a cross-check, not the thing that decides.
func (k Keyring) Verify(msg, sig []byte) (Key, error) {
	usable := k.Verifiers()
	if len(usable) == 0 {
		return Key{}, fmt.Errorf("no usable release key: every key in the ring is revoked")
	}
	for _, key := range usable {
		if ed25519.Verify(key.Pub, msg, sig) {
			return key, nil
		}
	}
	return Key{}, fmt.Errorf("signature matches none of the %d release key(s) this binary trusts (%s)",
		len(usable), strings.Join(usable.IDs(), ", "))
}

// IDs lists every fingerprint in the ring, for an error that says which keys were tried.
func (k Keyring) IDs() []string {
	out := make([]string, len(k))
	for i, key := range k {
		out[i] = key.ID
	}
	return out
}
