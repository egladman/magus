package selfupdate

import (
	"crypto/ed25519"
	_ "embed"
	"fmt"
)

//go:embed release.pub
var embeddedPubKey []byte

// PubKey is the embedded Ed25519 public key that verifies magus release signatures
// (SHA256SUMS.sig). Both magus itself (self-update) and magus-utils (release
// tooling, CI) verify against this one checked-in source.
var PubKey ed25519.PublicKey

func init() {
	if len(embeddedPubKey) != ed25519.PublicKeySize {
		panic(fmt.Sprintf(
			"selfupdate: embedded release public key is %d bytes, want %d; rebuild from a valid release",
			len(embeddedPubKey), ed25519.PublicKeySize,
		))
	}
	PubKey = ed25519.PublicKey(embeddedPubKey)
}
