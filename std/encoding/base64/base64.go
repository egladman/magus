// Package base64 is the "encoding/base64" host module: base64 lives under
// std/encoding rather than in std's own flat root because it, like its eight
// siblings, uses none of std's shared sandbox/exec helpers (resolvePath,
// checkRead, checkWrite, optStringDefault, ...) - every helper a text codec
// needs is local to its own file, so splitting it into its own package hides
// nothing that std itself needs to reach back into. See std/encoding/register.go
// for how this directory's Module reaches the rest of magus without std
// importing back down to collect it (that would cycle: std/encoding imports
// std for the Module vocabulary already).
package base64

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/egladman/magus/std"
)

//go:generate go run ../../../cmd/magus-utils bindings -module base64 -lang buzz -out ../../../internal/interp/bindings/gen/base64.go

// Module is the "encoding/base64" host module. Buzz's own stdlib hashes
// (crypto.hash) and serializes JSON but has no general-purpose binary-to-text
// codec, so a spell that signs a request or embeds a blob in a config would
// otherwise have to reimplement base64 in script.
//
// Inputs and outputs are plain strings: bytes cross as their raw string content
// (Buzz strings are byte-preserving for ASCII/UTF-8 payloads), the same shape
// crypto.*_hex consumes. crypto.base64Encode is the byte-list form for genuinely
// binary data.
var Module = std.Module{
	Name: "base64",
	WASM: true,
	Path: "encoding/base64",
	Doc:  "Base64 text codec (standard and URL-safe, both padded).",
	Methods: []std.Method{
		{
			Name:    "encode",
			Doc:     "Encode data as standard (padded) base64.",
			Args:    []std.Arg{{Name: "data", Type: std.TypeString}},
			Returns: []std.Ret{{Type: std.TypeString}},
			Impl:    Base64Encode,
		},
		{
			Name:    "decode",
			Doc:     "Decode a standard (padded) base64 string; errors on malformed input.",
			Args:    []std.Arg{{Name: "s", Type: std.TypeString}},
			Returns: []std.Ret{{Type: std.TypeString}},
			Raises:  true,
			Impl:    Base64Decode,
		},
		{
			Name:    "url_encode",
			Doc:     "Encode data as URL-safe (padded) base64.",
			Args:    []std.Arg{{Name: "data", Type: std.TypeString}},
			Returns: []std.Ret{{Type: std.TypeString}},
			Impl:    Base64URLEncode,
		},
		{
			Name:    "url_decode",
			Doc:     "Decode a URL-safe (padded) base64 string; errors on malformed input.",
			Args:    []std.Arg{{Name: "s", Type: std.TypeString}},
			Returns: []std.Ret{{Type: std.TypeString}},
			Raises:  true,
			Impl:    Base64URLDecode,
		},
	},
}

// Base64Encode encodes data as standard padded base64.
func Base64Encode(_ context.Context, data string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(data)), nil
}

// Base64Decode decodes a standard padded base64 string.
func Base64Decode(_ context.Context, s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("base64.decode: %w", err)
	}
	return string(b), nil
}

// Base64URLEncode encodes data as URL-safe padded base64.
func Base64URLEncode(_ context.Context, data string) (string, error) {
	return base64.URLEncoding.EncodeToString([]byte(data)), nil
}

// Base64URLDecode decodes a URL-safe padded base64 string.
func Base64URLDecode(_ context.Context, s string) (string, error) {
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("base64.url_decode: %w", err)
	}
	return string(b), nil
}
