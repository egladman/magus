package cache

import "fmt"

// SigningKeyEnv carries the base64 Ed25519 seed the remote tier signs with.
const SigningKeyEnv = "MAGUS_CACHE_SIGNING_KEY"

// ErrNoKeyGiven is returned by ResolveKeyInfo when there is neither a public key
// argument nor a seed in the environment.
var ErrNoKeyGiven = fmt.Errorf("provide a public key argument or set %s", SigningKeyEnv)

// SigningKeyRecord is the generated keypair as a record, so `-o template` can hand
// the seed to a secret store over a pipe. The JSON TAGS are the template fields
// (`-o template` projects the -o json names, so it is `{{.seed}}`, not
// `{{.Seed}}`), and renaming one breaks a documented command line. Treat the
// tags as API.
type SigningKeyRecord struct {
	KeyID  string `json:"keyid" yaml:"keyid"`
	Seed   string `json:"seed" yaml:"seed"`
	PubKey string `json:"pubkey" yaml:"pubkey"`
}

// NewSigningKeyRecord is km as the record `config cache key generate` emits.
func NewSigningKeyRecord(km KeyMaterial) SigningKeyRecord {
	return SigningKeyRecord{KeyID: km.KeyID, Seed: km.SeedB64, PubKey: km.PubB64}
}

// ResolveKeyInfo derives a key's public identity from the first of args when
// there is one, else from the seed getenv returns for SigningKeyEnv. It never
// returns the seed.
func ResolveKeyInfo(args []string, getenv func(string) string) (KeyInfo, error) {
	if len(args) >= 1 {
		return TrustedKeyInfo(args[0])
	}
	seed := getenv(SigningKeyEnv)
	if seed == "" {
		return KeyInfo{}, ErrNoKeyGiven
	}
	return SigningKeyInfo(seed)
}
