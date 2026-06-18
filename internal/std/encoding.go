package std

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
)

//go:generate go run ../../cmd/magus-bindings-gen -module encoding -lang buzz -out gen/buzz/encoding.go

func init() { Register(Encoding) }

// Encoding is the "encoding" host module: base64/hex/url text codecs. Buzz's own
// stdlib hashes (crypto.hash) and serializes JSON but has no general-purpose
// binary-to-text codec, so a spell that signs a request, embeds a blob in a
// config, or builds a query string would otherwise have to reimplement base64 in
// script. Inputs and outputs are plain strings: bytes cross as their raw string
// content (Buzz strings are byte-preserving for ASCII/UTF-8 payloads), the same
// shape crypto.*_hex consumes.
var Encoding = Module{
	Name: "encoding",
	Doc:  "Base64/hex/URL text codecs.",
	Methods: []Method{
		{
			Name:    "base64_encode",
			Doc:     "Encode data as standard (padded) base64.",
			Args:    []Arg{{Name: "data", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    EncodingBase64Encode,
		},
		{
			Name:    "base64_decode",
			Doc:     "Decode a standard (padded) base64 string; errors on malformed input.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    EncodingBase64Decode,
		},
		{
			Name:    "base64url_encode",
			Doc:     "Encode data as URL-safe (padded) base64.",
			Args:    []Arg{{Name: "data", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    EncodingBase64URLEncode,
		},
		{
			Name:    "base64url_decode",
			Doc:     "Decode a URL-safe (padded) base64 string; errors on malformed input.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    EncodingBase64URLDecode,
		},
		{
			Name:    "hex_encode",
			Doc:     "Encode data as lowercase hex.",
			Args:    []Arg{{Name: "data", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    EncodingHexEncode,
		},
		{
			Name:    "hex_decode",
			Doc:     "Decode a hex string; errors on malformed input.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    EncodingHexDecode,
		},
		{
			Name:    "url_encode",
			Doc:     "Percent-encode s for use in a URL query component.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    EncodingURLEncode,
		},
		{
			Name:    "url_decode",
			Doc:     "Decode a percent-encoded URL query component; errors on malformed input.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    EncodingURLDecode,
		},
		{
			Name:    "parse_url",
			Doc:     "Parse a URL string into {scheme, host, port, path, query, fragment}; errors on malformed input.",
			Args:    []Arg{{Name: "raw_url", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    EncodingParseURL,
		},
		{
			Name:    "build_url",
			Doc:     "Build a URL string from a {scheme, host, port, path, query, fragment} map; missing keys are treated as empty.",
			Args:    []Arg{{Name: "parts", Type: TypeAnyMap}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    EncodingBuildURL,
		},
	},
}

// EncodingBase64Encode encodes data as standard padded base64.
func EncodingBase64Encode(_ context.Context, data string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(data)), nil
}

// EncodingBase64Decode decodes a standard padded base64 string.
func EncodingBase64Decode(_ context.Context, s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("encoding.base64_decode: %w", err)
	}
	return string(b), nil
}

// EncodingBase64URLEncode encodes data as URL-safe padded base64.
func EncodingBase64URLEncode(_ context.Context, data string) (string, error) {
	return base64.URLEncoding.EncodeToString([]byte(data)), nil
}

// EncodingBase64URLDecode decodes a URL-safe padded base64 string.
func EncodingBase64URLDecode(_ context.Context, s string) (string, error) {
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("encoding.base64url_decode: %w", err)
	}
	return string(b), nil
}

// EncodingHexEncode encodes data as lowercase hex.
func EncodingHexEncode(_ context.Context, data string) (string, error) {
	return hex.EncodeToString([]byte(data)), nil
}

// EncodingHexDecode decodes a hex string.
func EncodingHexDecode(_ context.Context, s string) (string, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("encoding.hex_decode: %w", err)
	}
	return string(b), nil
}

// EncodingURLEncode percent-encodes s for a URL query component.
func EncodingURLEncode(_ context.Context, s string) (string, error) {
	return url.QueryEscape(s), nil
}

// EncodingURLDecode decodes a percent-encoded URL query component.
func EncodingURLDecode(_ context.Context, s string) (string, error) {
	v, err := url.QueryUnescape(s)
	if err != nil {
		return "", fmt.Errorf("encoding.url_decode: %w", err)
	}
	return v, nil
}

// EncodingParseURL parses rawURL into its components.
func EncodingParseURL(_ context.Context, rawURL string) (map[string]any, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("encoding.parse_url: %w", err)
	}
	return map[string]any{
		"scheme":   u.Scheme,
		"host":     u.Hostname(),
		"port":     u.Port(),
		"path":     u.Path,
		"query":    u.RawQuery,
		"fragment": u.Fragment,
	}, nil
}

// EncodingBuildURL assembles a URL string from a parts map with keys
// scheme, host, port, path, query, fragment. Missing keys are treated as empty.
func EncodingBuildURL(_ context.Context, parts map[string]any) (string, error) {
	strField := func(key string) string {
		if v, ok := parts[key].(string); ok {
			return v
		}
		return ""
	}
	u := &url.URL{
		Scheme:   strField("scheme"),
		Path:     strField("path"),
		RawQuery: strField("query"),
		Fragment: strField("fragment"),
	}
	host := strField("host")
	if port := strField("port"); port != "" {
		host = host + ":" + port
	}
	u.Host = host
	return u.String(), nil
}
