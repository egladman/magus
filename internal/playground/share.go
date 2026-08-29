package playground

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"io"
	"strings"
)

// A payload is one ASCII version byte followed by a base64url blob, so a decoder
// is never left sniffing which codec produced the fragment it was handed.
//
// Two versions coexist because two kinds of producer exist. The playground's
// Share button runs in Go and compresses; the docs site's "Open in Playground"
// links are built by the Buzz renderer (docs/site/tour.buzz) and the page bundle
// (docs/src/site/run-example.ts), neither of which has a DEFLATE implementation
// to reach for. Tagging the uncompressed form rather than leaving it bare is what
// keeps DecodeShare the one decoder for every producer.
const (
	shareRaw     = "0" // base64url of the source itself
	shareDeflate = "1" // base64url of a raw DEFLATE stream
)

// shareKey is the URL fragment parameter carrying the payload. A fragment, not a
// query parameter: browsers never send it to a server, so a shared snippet stays
// out of the origin's logs, the CDN, and the referrer header.
const shareKey = "source"

// EncodeShare packs a magusfile into a compact, URL-fragment-safe string so it
// can be shared by link with no server involved: the snippet rides entirely in
// the URL. It DEFLATEs the source (the sample magusfile shrinks from ~3.4KB of
// raw base64 to ~1.5KB) and base64url-encodes the result so it needs no
// escaping in a URL, then prefixes shareDeflate. It is the inverse of
// DecodeShare.
func EncodeShare(src string) (string, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(w, src); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return shareDeflate + base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}

// DecodeShare reads a payload from either producer: the compressed form
// EncodeShare emits, and the uncompressed shareRaw form the docs site's
// deep-links carry. It returns ok=false for any malformed input (missing or
// unknown version, invalid base64, corrupt DEFLATE stream), so a caller seeded
// from an untrusted URL fragment can silently fall back to the default magusfile
// rather than surface an error to the visitor.
func DecodeShare(s string) (src string, ok bool) {
	if s == "" {
		return "", false
	}
	version, body := s[:1], s[1:]
	if version != shareRaw && version != shareDeflate {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", false
	}
	if version == shareRaw {
		return string(raw), true
	}
	r := flate.NewReader(bytes.NewReader(raw))
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// ShareFragment renders src as the complete URL fragment (leading "#") that
// reopens it in the playground. The Share button assigns the result to the page's
// location, so the fragment shape lives here beside the parser that reads it back
// rather than being spelled out at each end.
func ShareFragment(src string) (string, error) {
	enc, err := EncodeShare(src)
	if err != nil {
		return "", err
	}
	return "#" + shareKey + "=" + enc, nil
}

// SourceFromFragment pulls the shared magusfile out of a location.hash, with or
// without its leading "#", ignoring any other parameters in the fragment. It
// returns ok=false when the fragment carries no source or one that will not
// decode; a visitor did not author the URL and cannot fix it, so the boot path
// keeps the seeded example instead of reporting an error.
func SourceFromFragment(hash string) (src string, ok bool) {
	for _, part := range strings.Split(strings.TrimPrefix(hash, "#"), "&") {
		k, v, found := strings.Cut(part, "=")
		if !found || k != shareKey {
			continue
		}
		return DecodeShare(v)
	}
	return "", false
}
