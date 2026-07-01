package playground

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"io"
	"strings"
)

// shareVersion tags an encoded payload so the wire format can change later
// without an old link being misread by a newer decoder (or the reverse). It is
// a single ASCII byte prefixed to the base64url blob.
const shareVersion = "1"

// EncodeShare packs a magusfile into a compact, URL-fragment-safe string so it
// can be shared by link with no server involved: the snippet rides entirely in
// the URL. It DEFLATEs the source (the sample magusfile shrinks from ~3.4KB of
// raw base64 to ~1.5KB) and base64url-encodes the result so it needs no
// escaping in a URL, then prefixes shareVersion. It is the inverse of
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
	return shareVersion + base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}

// DecodeShare reverses EncodeShare. It returns ok=false for any malformed input
// (missing or unknown version, invalid base64, corrupt DEFLATE stream), so a
// caller seeded from an untrusted URL fragment can silently fall back to the
// default magusfile rather than surface an error to the visitor.
func DecodeShare(s string) (src string, ok bool) {
	if !strings.HasPrefix(s, shareVersion) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(s[len(shareVersion):])
	if err != nil {
		return "", false
	}
	r := flate.NewReader(bytes.NewReader(raw))
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return "", false
	}
	return string(out), true
}
