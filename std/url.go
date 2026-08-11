package std

import (
	"context"
	"fmt"
	"net/url"

	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module url -lang buzz -out ../internal/interp/bindings/gen/url.go

func init() { Register(URL) }

// URL is the "encoding/url" host module: percent-encoding, plus parsing a URL
// into its components and building one back out.
//
// Filed under encoding/ rather than Go's net/url because percent-encoding IS a
// text codec and belongs beside base64 and hex, and because magus has no net/
// tree for a single module to justify. parse and build round-trip through the
// same URL object, so a caller can take a URL apart, change one field, and put it
// back without string surgery on a query string.
var URL = Module{
	Name: "url",
	Path: "encoding/url",
	Doc:  "URL percent-encoding, parsing, and building.",
	Methods: []Method{
		{
			Name:    "encode",
			Doc:     "Percent-encode s for use in a URL query component.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    URLEncode,
		},
		{
			Name:    "decode",
			Doc:     "Decode a percent-encoded URL query component; errors on malformed input.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    URLDecode,
		},
		{
			Name:    "parse",
			Doc:     "Parse a URL string into {scheme, host, port, path, query, fragment}; errors on malformed input.",
			Args:    []Arg{{Name: "raw_url", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "URL"}},
			Impl:    URLParse,
		},
		{
			Name:    "build",
			Doc:     "Build a URL string from a URL object - the same shape parse returns, so the two round-trip. Missing fields are treated as empty.",
			Args:    []Arg{{Name: "parts", Type: TypeAnyMap, Object: "URL"}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    URLBuild,
		},
	},
}

// URLEncode percent-encodes s for a URL query component.
func URLEncode(_ context.Context, s string) (string, error) {
	return url.QueryEscape(s), nil
}

// URLDecode decodes a percent-encoded URL query component.
func URLDecode(_ context.Context, s string) (string, error) {
	v, err := url.QueryUnescape(s)
	if err != nil {
		return "", fmt.Errorf("url.decode: %w", err)
	}
	return v, nil
}

// URLParse parses rawURL into its components.
func URLParse(_ context.Context, rawURL string) (types.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return types.URL{}, fmt.Errorf("url.parse: %w", err)
	}
	return types.URL{
		Scheme:   u.Scheme,
		Host:     u.Hostname(),
		Port:     u.Port(),
		Path:     u.Path,
		Query:    u.RawQuery,
		Fragment: u.Fragment,
	}, nil
}

// URLBuild assembles a URL string from a parts map with keys scheme, host, port,
// path, query, fragment. Missing keys are treated as empty.
func URLBuild(_ context.Context, parts map[string]any) (string, error) {
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
