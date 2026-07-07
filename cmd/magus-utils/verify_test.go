package main

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerifyHappyPath signs a file with a deterministic keypair, injects the matching
// public key via overrideVerifyPubKey (the embedded release key has no private
// counterpart in this repo), and confirms runVerify accepts the genuine signature.
func TestVerifyHappyPath(t *testing.T) {
	priv := ed25519.NewKeyFromSeed(signTestSeed)
	pub := priv.Public().(ed25519.PublicKey)

	dir := t.TempDir()
	target := filepath.Join(dir, "SHA256SUMS")
	payload := []byte("deadbeef  magus_v1.0.0_linux_amd64.tar.gz\n")
	require.NoError(t, os.WriteFile(target, payload, 0o644), "write target")

	sig := ed25519.Sign(priv, payload)
	sigPath := target + ".sig"
	require.NoError(t, os.WriteFile(sigPath, sig, 0o644), "write sig")

	t.Cleanup(func() { overrideVerifyPubKey = nil })
	overrideVerifyPubKey = pub

	require.NoError(t, runVerify([]string{target, sigPath}), "runVerify happy path")
}

// TestVerifyErrors table-drives the failure paths.
func TestVerifyErrors(t *testing.T) {
	priv := ed25519.NewKeyFromSeed(signTestSeed)
	pub := priv.Public().(ed25519.PublicKey)

	dir := t.TempDir()
	target := filepath.Join(dir, "SHA256SUMS")
	payload := []byte("deadbeef  magus_v1.0.0_linux_amd64.tar.gz\n")
	require.NoError(t, os.WriteFile(target, payload, 0o644), "write target")

	sigPath := target + ".sig"
	require.NoError(t, os.WriteFile(sigPath, ed25519.Sign(priv, payload), 0o644), "write sig")

	tamperedSigPath := filepath.Join(dir, "tampered.sig")
	require.NoError(t, os.WriteFile(tamperedSigPath, ed25519.Sign(priv, []byte("not the payload")), 0o644), "write tampered sig")

	shortSigPath := filepath.Join(dir, "short.sig")
	require.NoError(t, os.WriteFile(shortSigPath, []byte("too short"), 0o644), "write short sig")

	t.Cleanup(func() { overrideVerifyPubKey = nil })
	overrideVerifyPubKey = pub

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "no args",
			args:    []string{},
			wantErr: "usage: magus-utils verify <file> <sigfile>",
		},
		{
			name:    "too many args",
			args:    []string{"a", "b", "c"},
			wantErr: "usage: magus-utils verify <file> <sigfile>",
		},
		{
			name:    "missing target file",
			args:    []string{filepath.Join(dir, "does-not-exist"), sigPath},
			wantErr: "read ",
		},
		{
			name:    "missing sig file",
			args:    []string{target, filepath.Join(dir, "does-not-exist.sig")},
			wantErr: "read ",
		},
		{
			name:    "wrong sig size",
			args:    []string{target, shortSigPath},
			wantErr: "want 64",
		},
		{
			name:    "signature does not match file",
			args:    []string{target, tamperedSigPath},
			wantErr: "signature check failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := runVerify(tc.args)
			require.Error(t, err, "expected an error")
			assert.Contains(t, err.Error(), tc.wantErr, "error message mismatch")
		})
	}
}
