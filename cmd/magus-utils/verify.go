package main

import (
	"crypto/ed25519"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/selfupdate"
)

// overrideVerifyPubKey lets tests substitute a known keypair for the embedded
// release key, which has no corresponding private key in this repo.
var overrideVerifyPubKey ed25519.PublicKey

// runVerify checks a detached Ed25519 signature (e.g. SHA256SUMS.sig) against
// the embedded release public key. It's the counterpart to runSign, used by
// release_sign() in magusfile.buzz (CD's publish job) to self-check a signature
// against the same key baked into the magus binary that verifies self-updates.
//
// Usage:
//
//	magus-utils verify <file> <sigfile>
func runVerify(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: magus-utils verify <file> <sigfile>")
	}
	path, sigPath := args[0], args[1]

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", sigPath, err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%s is %d bytes, want %d", sigPath, len(sig), ed25519.SignatureSize)
	}

	pubKey, err := releaseVerifyKey()
	if err != nil {
		return err
	}
	if !ed25519.Verify(pubKey, data, sig) {
		return fmt.Errorf("signature check failed: %s does not match %s", sigPath, path)
	}
	fmt.Printf("verified %s against %s\n", sigPath, path)
	return nil
}

// releaseVerifyKey is the key a release self-check verifies against: the ring's
// ACTIVE key, not the whole ring. The ring exists so a client can accept a signature
// from a key that is not the current signer; a release that just signed something has
// no such latitude, and accepting a standby key here would let a mis-set
// MAGUS_SIGNING_KEY publish quietly.
func releaseVerifyKey() (ed25519.PublicKey, error) {
	if overrideVerifyPubKey != nil {
		return overrideVerifyPubKey, nil
	}
	active, err := selfupdate.ReleaseKeys.Active()
	if err != nil {
		return nil, err
	}
	return active.Pub, nil
}
