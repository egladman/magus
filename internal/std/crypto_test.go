package std

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Known SHA-256 vectors from FIPS 180-4 / RFC examples.
const (
	sha256Empty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	sha256ABC   = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
)

func TestCryptoSha256Hex(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", sha256Empty},
		{"abc", sha256ABC},
	} {
		got, err := CryptoSha256Hex(context.Background(), tc.in)
		if err != nil {
			t.Fatalf("CryptoSha256Hex(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("CryptoSha256Hex(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestCryptoSha256File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := CryptoSha256File(context.Background(), path)
	if err != nil {
		t.Fatalf("CryptoSha256File: %v", err)
	}
	if got != sha256ABC {
		t.Errorf("CryptoSha256File = %s, want %s", got, sha256ABC)
	}
}

func TestCryptoSha256FileMissing(t *testing.T) {
	if _, err := CryptoSha256File(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for a missing file, got nil")
	}
}

// Known digests of "abc"/"" from the standard test vectors.
func TestCryptoDigests(t *testing.T) {
	cases := []struct {
		name string
		fn   func(context.Context, string) (string, error)
		in   string
		want string
	}{
		{"sha512/abc", CryptoSha512Hex, "abc", "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"},
		{"sha1/abc", CryptoSha1Hex, "abc", "a9993e364706816aba3e25717850c26c9cd0d89d"},
		{"sha1/empty", CryptoSha1Hex, "", "da39a3ee5e6b4b0d3255bfef95601890afd80709"},
		{"md5/abc", CryptoMd5Hex, "abc", "900150983cd24fb0d6963f7d28e17f72"},
		{"md5/empty", CryptoMd5Hex, "", "d41d8cd98f00b204e9800998ecf8427e"},
	}
	for _, tc := range cases {
		got, err := tc.fn(context.Background(), tc.in)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// TestCryptoSha512File exercises hashFile through one of the new algorithms.
func TestCryptoSha512File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := CryptoSha512File(context.Background(), path)
	if err != nil {
		t.Fatalf("CryptoSha512File: %v", err)
	}
	const sha512ABC = "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"
	if got != sha512ABC {
		t.Errorf("CryptoSha512File = %s, want %s", got, sha512ABC)
	}
}
