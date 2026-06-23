package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signTestSeed is a fixed 32-byte ed25519 seed so the derived key (and thus the
// test) is deterministic. ed25519.NewKeyFromSeed expands it into the 64-byte
// private key (seed || pub) that runSign expects as 128 hex chars.
var signTestSeed = []byte{
	0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
	0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
	0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
}

// TestSignHappyPath generates a deterministic key, signs a temp file via runSign,
// and verifies the emitted <file>.sig validates against the public key.
func TestSignHappyPath(t *testing.T) {
	priv := ed25519.NewKeyFromSeed(signTestSeed)
	pub := priv.Public().(ed25519.PublicKey)
	require.Len(t, priv, ed25519.PrivateKeySize, "private key size")

	t.Setenv("MAGUS_SIGNING_KEY", hex.EncodeToString(priv))

	dir := t.TempDir()
	target := filepath.Join(dir, "SHA256SUMS")
	payload := []byte("deadbeef  artifact.tar.gz\n")
	require.NoError(t, os.WriteFile(target, payload, 0o644), "write target")

	require.NoError(t, runSign([]string{target}), "runSign happy path")

	sigPath := target + ".sig"
	sig, err := os.ReadFile(sigPath)
	require.NoError(t, err, "read emitted .sig")
	assert.Len(t, sig, ed25519.SignatureSize, "signature length")

	assert.True(t, ed25519.Verify(pub, payload, sig),
		"emitted signature does not verify against the public key")

	// A signature over different data must NOT verify, confirming the .sig is
	// bound to the file's actual contents.
	assert.False(t, ed25519.Verify(pub, []byte("tampered"), sig),
		"signature unexpectedly verified against tampered data")
}

// TestSignErrors table-drives the failure paths, asserting the specific message
// runSign returns for each. validHexKey is the deterministic 128-char key reused
// by cases where the key itself is valid but something else is wrong.
func TestSignErrors(t *testing.T) {
	validHexKey := hex.EncodeToString(ed25519.NewKeyFromSeed(signTestSeed))

	// A file that exists, for cases isolating non-file failures (wrong arg count,
	// bad key) from the missing-target case.
	existingDir := t.TempDir()
	existingFile := filepath.Join(existingDir, "present.txt")
	require.NoError(t, os.WriteFile(existingFile, []byte("hi"), 0o644), "write existing file")

	tests := []struct {
		name    string
		key     string // value for MAGUS_SIGNING_KEY (set via t.Setenv, "" means empty)
		args    []string
		wantErr string // substring the returned error must contain
	}{
		{
			name:    "no args",
			key:     validHexKey,
			args:    []string{},
			wantErr: "usage: magus-utils sign <file>",
		},
		{
			name:    "too many args",
			key:     validHexKey,
			args:    []string{"a", "b"},
			wantErr: "usage: magus-utils sign <file>",
		},
		{
			name:    "empty env",
			key:     "",
			args:    []string{existingFile},
			wantErr: "MAGUS_SIGNING_KEY is not set",
		},
		{
			name:    "invalid hex",
			key:     "zzzz" + validHexKey[4:], // non-hex chars, correct length
			args:    []string{existingFile},
			wantErr: "decode MAGUS_SIGNING_KEY",
		},
		{
			name:    "wrong key length (too short)",
			key:     hex.EncodeToString(signTestSeed), // 32 bytes, not 64
			args:    []string{existingFile},
			wantErr: "MAGUS_SIGNING_KEY must be 64 bytes",
		},
		{
			name:    "missing target file",
			key:     validHexKey,
			args:    []string{filepath.Join(t.TempDir(), "does-not-exist")},
			wantErr: "read ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// t.Setenv on "" guarantees the var is empty regardless of the ambient
			// environment, and restores it after the subtest.
			t.Setenv("MAGUS_SIGNING_KEY", tc.key)

			err := runSign(tc.args)
			require.Error(t, err, "expected an error")
			assert.Contains(t, err.Error(), tc.wantErr, "error message mismatch")
		})
	}
}
