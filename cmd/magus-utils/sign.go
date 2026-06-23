package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
)

// runSign signs a file with an Ed25519 private key and writes the detached
// signature alongside it (e.g. SHA256SUMS -> SHA256SUMS.sig).
//
// The private key is read from MAGUS_SIGNING_KEY as a 128-character lowercase
// hex string (64 raw bytes: 32-byte seed + 32-byte pub).
//
// Usage: magus-utils sign <file>
func runSign(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: magus-utils sign <file>")
	}
	path := args[0]

	keyHex := os.Getenv("MAGUS_SIGNING_KEY")
	if keyHex == "" {
		return fmt.Errorf("MAGUS_SIGNING_KEY is not set")
	}
	keyBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		return fmt.Errorf("decode MAGUS_SIGNING_KEY: %w", err)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		return fmt.Errorf("MAGUS_SIGNING_KEY must be %d bytes (%d hex chars), got %d bytes",
			ed25519.PrivateKeySize, ed25519.PrivateKeySize*2, len(keyBytes))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	sig := ed25519.Sign(ed25519.PrivateKey(keyBytes), data)

	outPath := path + ".sig"
	if err := os.WriteFile(outPath, sig, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Printf("signed %s -> %s\n", path, outPath)
	return nil
}
