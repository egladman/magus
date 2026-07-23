package httpx

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// Gzip wraps next so a response BODY is gzip-compressed when the client advertises
// Accept-Encoding: gzip. It sets Content-Encoding and appends Accept-Encoding to Vary, and
// drops any Content-Length the inner handler set (the compressed length differs). Only 200
// responses are compressed; a bodyless status (304/204) passes through untouched, so a
// conditional GET still returns an empty 304. An inner handler that already set an ETag keeps
// it - the tag is computed over the uncompressed body and is identical for both encodings.
func Gzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		gz := gzip.NewWriter(w)
		gw := &gzipResponseWriter{ResponseWriter: w, gz: gz}
		defer func() { _ = gw.close() }()
		next.ServeHTTP(gw, r)
	})
}

// gzipResponseWriter compresses only 200 bodies; other statuses pass through so a 304 stays
// empty. The gzip writer is created up front but engaged lazily at WriteHeader time.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
	useGzip     bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	if code == http.StatusOK {
		g.useGzip = true
		h := g.Header()
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		h.Del("Content-Length")
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if g.useGzip {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipResponseWriter) close() error {
	if g.useGzip {
		return g.gz.Close()
	}
	return nil
}

// acceptsGzip reports whether the request accepts a gzip-encoded response, parsing the
// comma-separated Accept-Encoding value and ignoring any ;q= quality suffix.
func acceptsGzip(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept-Encoding") {
		for _, tok := range strings.Split(v, ",") {
			if coding, _, _ := strings.Cut(tok, ";"); strings.TrimSpace(coding) == "gzip" {
				return true
			}
		}
	}
	return false
}
