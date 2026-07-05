package std

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
)

//go:generate go run ../cmd/magus-utils bindings -module uuid -lang buzz -out ../host/gen/uuid.go

func init() { Register(UUID) }

// UUID is the "uuid" host module: unique identifiers and random tokens for run
// ids, cache-busting suffixes, and artifact names. Native Buzz can only produce a
// bounded random int, so anything needing a collision-free id previously reached
// for os.time() hacks. Host-only (excluded from the browser playground) so its
// nondeterminism never leaks into the "planned, not run" dry-run surface.
var UUID = Module{
	Name: "uuid",
	Doc:  "Unique identifiers and random tokens (v4 random, v7 time-ordered, plus raw random hex/tokens).",
	Methods: []Method{
		{
			Name:    "v4",
			Doc:     "A random (version 4) UUID string, e.g. \"9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d\".",
			Returns: []Ret{{Type: TypeString}},
			Impl:    UUIDv4,
		},
		{
			Name:    "v7",
			Doc:     "A time-ordered (version 7) UUID string; lexically sorts by creation time, which makes it a good ordered run/build id.",
			Returns: []Ret{{Type: TypeString}},
			Impl:    UUIDv7,
		},
		{
			Name:    "randomHex",
			Doc:     "A cryptographically random lowercase hex string of n bytes (2*n characters); errors when n is not positive.",
			Args:    []Arg{{Name: "n", Type: TypeInt}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    UUIDRandomHex,
		},
		{
			Name:    "randomToken",
			Doc:     "A cryptographically random URL-safe base64 token from n bytes of entropy (no padding); errors when n is not positive.",
			Args:    []Arg{{Name: "n", Type: TypeInt}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    UUIDRandomToken,
		},
	},
}

// UUIDv4 returns a random version-4 UUID.
func UUIDv4(_ context.Context) (string, error) {
	u, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("uuid.v4: %w", err)
	}
	return u.String(), nil
}

// UUIDv7 returns a time-ordered version-7 UUID.
func UUIDv7(_ context.Context) (string, error) {
	u, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("uuid.v7: %w", err)
	}
	return u.String(), nil
}

// UUIDRandomHex returns n random bytes as a lowercase hex string.
func UUIDRandomHex(_ context.Context, n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("uuid.randomHex: n must be positive, got %d", n)
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("uuid.randomHex: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// UUIDRandomToken returns n random bytes as an unpadded URL-safe base64 string.
func UUIDRandomToken(_ context.Context, n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("uuid.randomToken: n must be positive, got %d", n)
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("uuid.randomToken: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
