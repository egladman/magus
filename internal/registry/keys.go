package registry

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"

	json "github.com/egladman/magus/internal/json"
)

//go:embed registry-keys.json
var embeddedKeys []byte

// PinnedKeys is the set of Ed25519 public keys the BUILT-IN registry source is
// verified against, pinned in every binary the way the release key is.
//
// A SEPARATE keyring from internal/selfupdate's, and that separation is the point.
// The release key signs magus binaries and is reachable from exactly one job on a
// v* tag; the registry is rebuilt daily from several hundred third-party HTTP
// responses. Sharing one key would put the binary-signing key in the job with the
// widest input surface in the project.
//
// It is EMPTY until the registry keypair exists. That is deliberate rather than a
// placeholder: an empty ring makes `magus self refresh` refuse the built-in source
// with a clear reason, which is the correct behavior for a binary that has been
// given no way to check what it would download. Filling it in is a one-line change
// plus the matching MAGUS_REGISTRY_KEY secret.
var PinnedKeys []ed25519.PublicKey

type keyFile struct {
	Keys []string `json:"keys"`
}

func init() {
	var f keyFile
	if err := json.Unmarshal(embeddedKeys, &f); err != nil {
		panic("registry: embedded registry-keys.json: " + err.Error())
	}
	for i, h := range f.Keys {
		raw, err := decodeHexKey(strings.TrimSpace(h))
		if err != nil {
			panic(fmt.Sprintf("registry: embedded key %d: %v", i, err))
		}
		PinnedKeys = append(PinnedKeys, ed25519.PublicKey(raw))
	}
}

// pinnedHex renders the pinned keys for an error that has to say what was tried.
func pinnedHex() string {
	out := make([]string, len(PinnedKeys))
	for i, k := range PinnedKeys {
		out[i] = hex.EncodeToString(k)
	}
	return strings.Join(out, ", ")
}
