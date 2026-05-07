package bindings

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
)

// These tests exercise the real spells/aws/s3-cache/spell.bzz against an emulator that
// independently recomputes the AWS SigV4 signature for every request and rejects
// a mismatch — the same check S3 performs. The signing-key chain is already
// verified against AWS's published vector in internal/std/extra/crypto; here we
// validate the spell's canonical-request and string-to-sign construction by
// cross-checking it with a second, independent (Go) implementation.

func s3Backend(t *testing.T) *spellRemoteBackend {
	t.Helper()
	path := filepath.Join(repoRoot(t), "magus", "spells", "aws", "s3-cache", "spell.bzz")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("s3 spell not found at %s: %v", path, err)
	}
	drv, err := resolveBackendSpell(context.Background(), path)
	if err != nil {
		t.Fatalf("load s3 spell: %v", err)
	}
	if drv.Name() != "s3-cache" {
		t.Fatalf("spell name = %q, want s3-cache", drv.Name())
	}
	return &spellRemoteBackend{drv: drv}
}

type s3Emulator struct {
	mu      sync.Mutex
	objects map[string][]byte
	mtimes  map[string]time.Time // S3 LastModified per object path; for prune listing
	bucket  string               // bucket name, used to scope the listing
	secret  string
	region  string
	t       *testing.T
}

func (e *s3Emulator) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := e.verifySigV4(r, body); err != nil {
			e.t.Errorf("SigV4 verification failed for %s %s: %v", r.Method, r.URL.Path, err)
			http.Error(w, "SignatureDoesNotMatch", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodPut:
			// S3 verifies the body against x-amz-content-sha256; mirror that.
			if got, want := sha256Hex(body), r.Header.Get("x-amz-content-sha256"); got != want {
				e.t.Errorf("payload hash mismatch: body=%s header=%s", got, want)
				http.Error(w, "XAmzContentSHA256Mismatch", http.StatusBadRequest)
				return
			}
			e.mu.Lock()
			e.objects[r.URL.Path] = body
			e.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			// A bucket-scoped GET carrying ?prefix= is a ListObjects request; a GET
			// with an object key in the path is a download.
			if r.URL.Query().Has("prefix") {
				e.writeListing(w, r)
				return
			}
			e.mu.Lock()
			data, ok := e.objects[r.URL.Path]
			e.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(data)
		case http.MethodDelete:
			e.mu.Lock()
			delete(e.objects, r.URL.Path)
			delete(e.mtimes, r.URL.Path)
			e.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

// verifySigV4 recomputes the request's SigV4 signature from scratch and compares
// it to the Authorization header — an independent check of the spell's signing.
func (e *s3Emulator) verifySigV4(r *http.Request, body []byte) error {
	auth := r.Header.Get("Authorization")
	const algo = "AWS4-HMAC-SHA256 "
	if !strings.HasPrefix(auth, algo) {
		return fmt.Errorf("missing/!AWS4 Authorization: %q", auth)
	}
	var cred, signed, sig string
	for _, part := range strings.Split(strings.TrimPrefix(auth, algo), ", ") {
		switch {
		case strings.HasPrefix(part, "Credential="):
			cred = strings.TrimPrefix(part, "Credential=")
		case strings.HasPrefix(part, "SignedHeaders="):
			signed = strings.TrimPrefix(part, "SignedHeaders=")
		case strings.HasPrefix(part, "Signature="):
			sig = strings.TrimPrefix(part, "Signature=")
		}
	}
	// Credential = <access>/<datestamp>/<region>/s3/aws4_request
	credParts := strings.SplitN(cred, "/", 2)
	if len(credParts) != 2 {
		return fmt.Errorf("bad Credential: %q", cred)
	}
	scope := credParts[1]
	datestamp := strings.SplitN(scope, "/", 2)[0]
	amzdate := r.Header.Get("x-amz-date")
	payloadHash := r.Header.Get("x-amz-content-sha256")

	// Validate the spell's pure-Buzz UTC formatter end-to-end: x-amz-date must
	// parse and sit within S3's ±15-minute skew window. A wrong calendar split
	// would land far outside it (the same way real S3 would reject the request).
	ts, err := time.Parse("20060102T150405Z", amzdate)
	if err != nil {
		return fmt.Errorf("x-amz-date %q does not parse: %w", amzdate, err)
	}
	if d := time.Since(ts); d > 15*time.Minute || d < -15*time.Minute {
		return fmt.Errorf("x-amz-date %q skew %v exceeds ±15m (formatter wrong?)", amzdate, d)
	}

	// Canonical headers, in the order named by SignedHeaders.
	var hb strings.Builder
	for _, name := range strings.Split(signed, ";") {
		var val string
		switch name {
		case "host":
			val = r.Host
		default:
			val = r.Header.Get(name)
		}
		fmt.Fprintf(&hb, "%s:%s\n", name, val)
	}
	canonicalRequest := r.Method + "\n" +
		r.URL.EscapedPath() + "\n" +
		canonicalQuery(r.URL.Query()) + "\n" +
		hb.String() + "\n" +
		signed + "\n" +
		payloadHash
	stringToSign := "AWS4-HMAC-SHA256\n" + amzdate + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))

	kDate := hmacSum([]byte("AWS4"+e.secret), datestamp)
	kRegion := hmacSum(kDate, e.region)
	kService := hmacSum(kRegion, "s3")
	kSigning := hmacSum(kService, "aws4_request")
	want := hex.EncodeToString(hmacSum(kSigning, stringToSign))
	if want != sig {
		return fmt.Errorf("signature mismatch:\n want %s\n got  %s\n canonicalRequest=%q", want, sig, canonicalRequest)
	}
	return nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hmacSum(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// canonicalQuery rebuilds the SigV4 canonical query string (keys sorted, values
// AWS-URI-encoded) — the same construction the spell relies on. It returns "" for a
// query-less request, preserving the original PUT/GET object signing.
func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, awsURIEncode(k)+"="+awsURIEncode(v))
		}
	}
	return strings.Join(parts, "&")
}

func awsURIEncode(s string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if c := s[i]; strings.IndexByte(unreserved, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// writeListing serves a ListObjects v1 page for the magus- prefix, honoring the
// marker and capping each page at two keys so a >2-object bucket truncates — which
// drives the spell's listing fiber across multiple pages.
func (e *s3Emulator) writeListing(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	marker := r.URL.Query().Get("marker")
	bucketPath := "/" + e.bucket + "/"

	e.mu.Lock()
	var keys []string
	for path := range e.objects {
		key, ok := strings.CutPrefix(path, bucketPath)
		if !ok || !strings.HasPrefix(key, prefix) {
			continue
		}
		if _, dated := e.mtimes[path]; !dated {
			continue // a real S3 object always has a LastModified; skip undated seeds
		}
		if marker != "" && key <= marker { // marker is exclusive, lexicographic
			continue
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)

	const pageSize = 2
	truncated := len(keys) > pageSize
	if truncated {
		keys = keys[:pageSize]
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult>`)
	fmt.Fprintf(&b, "<Name>%s</Name><Prefix>%s</Prefix><IsTruncated>%t</IsTruncated>", e.bucket, prefix, truncated)
	for _, k := range keys {
		mt := e.mtimes["/"+e.bucket+"/"+k]
		fmt.Fprintf(&b, "<Contents><Key>%s</Key><LastModified>%s</LastModified><Size>%d</Size></Contents>",
			k, mt.UTC().Format("2006-01-02T15:04:05.000Z"), len(e.objects["/"+e.bucket+"/"+k]))
	}
	b.WriteString(`</ListBucketResult>`)
	e.mu.Unlock()

	_, _ = w.Write([]byte(b.String()))
}

// TestS3Prune exercises the spell's prune op end-to-end against the emulator: the
// listing fiber pages through a truncating bucket, and the age/count bounds select
// the objects deleted. The emulator independently re-signs every list/delete, so
// SigV4 over a query string is validated too.
func TestS3Prune(t *testing.T) {
	now := time.Now().UTC()

	// newStore seeds a fresh emulator + env and returns the backend plus a snapshot
	// reader of the magus- keys still present.
	newStore := func(t *testing.T) (*spellRemoteBackend, *s3Emulator) {
		emu := &s3Emulator{
			objects: map[string][]byte{}, mtimes: map[string]time.Time{},
			bucket: "magus-cache", secret: "test-secret-key", region: "us-east-1", t: t,
		}
		srv := httptest.NewServer(emu.handler())
		t.Cleanup(srv.Close)
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIDTEST")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
		t.Setenv("AWS_REGION", "us-east-1")
		t.Setenv("MAGUS_S3_BUCKET", "magus-cache")
		t.Setenv("MAGUS_S3_ENDPOINT", srv.URL)

		seed := func(key string, age time.Duration) {
			p := "/magus-cache/" + key
			emu.objects[p] = []byte("x")
			emu.mtimes[p] = now.Add(-age)
		}
		// Five magus- objects across a span of ages (forces 3 listing pages at
		// pageSize 2), plus one non-magus object the prefix scope must never touch.
		seed("magus-aaa-magus-remote-v1", 30*24*time.Hour)
		seed("magus-bbb-magus-remote-v1", 20*24*time.Hour)
		seed("magus-ccc-magus-remote-v1", 10*24*time.Hour)
		seed("magus-ddd-magus-remote-v1", 2*24*time.Hour)
		seed("magus-eee-magus-remote-v1", 1*time.Hour)
		emu.objects["/magus-cache/other-key"] = []byte("x")
		emu.mtimes["/magus-cache/other-key"] = now.Add(-365 * 24 * time.Hour)

		return s3Backend(t), emu
	}

	remaining := func(emu *s3Emulator) []string {
		emu.mu.Lock()
		defer emu.mu.Unlock()
		var ks []string
		for p := range emu.objects {
			ks = append(ks, strings.TrimPrefix(p, "/magus-cache/"))
		}
		slices.Sort(ks)
		return ks
	}

	t.Run("age", func(t *testing.T) {
		store, emu := newStore(t)
		// Older than 7 days → aaa(30d), bbb(20d), ccc(10d) evicted.
		if err := store.PruneArtifacts(context.Background(), cache.RetentionPolicy{OlderThan: 7 * 24 * time.Hour}); err != nil {
			t.Fatalf("PruneArtifacts(age): %v", err)
		}
		got := remaining(emu)
		want := []string{"magus-ddd-magus-remote-v1", "magus-eee-magus-remote-v1", "other-key"}
		if !slices.Equal(got, want) {
			t.Fatalf("after age prune, remaining = %v, want %v", got, want)
		}
	})

	t.Run("count", func(t *testing.T) {
		store, emu := newStore(t)
		// Keep newest 2 → eee(1h), ddd(2d) kept; ccc, bbb, aaa evicted.
		if err := store.PruneArtifacts(context.Background(), cache.RetentionPolicy{KeepLast: 2}); err != nil {
			t.Fatalf("PruneArtifacts(count): %v", err)
		}
		got := remaining(emu)
		want := []string{"magus-ddd-magus-remote-v1", "magus-eee-magus-remote-v1", "other-key"}
		if !slices.Equal(got, want) {
			t.Fatalf("after count prune, remaining = %v, want %v", got, want)
		}
	})

	t.Run("dry-run deletes nothing", func(t *testing.T) {
		store, emu := newStore(t)
		before := remaining(emu)
		if err := store.PruneArtifacts(context.Background(), cache.RetentionPolicy{OlderThan: time.Hour, DryRun: true}); err != nil {
			t.Fatalf("PruneArtifacts(dry-run): %v", err)
		}
		if got := remaining(emu); !slices.Equal(got, before) {
			t.Fatalf("dry run mutated the bucket: %v != %v", got, before)
		}
	})
}

func TestS3CacheBackendRoundTrip(t *testing.T) {
	emu := &s3Emulator{objects: map[string][]byte{}, secret: "test-secret-key", region: "us-east-1", t: t}
	srv := httptest.NewServer(emu.handler())
	defer srv.Close()

	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDTEST")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("MAGUS_S3_BUCKET", "magus-cache")
	t.Setenv("MAGUS_S3_ENDPOINT", srv.URL)

	store := s3Backend(t)
	if !store.Active(context.Background()) {
		t.Fatal("Active() = false with credentials + bucket set, want true")
	}
	ctx := context.Background()
	entry := bytes.Repeat([]byte{0x00, 0x1f, 0x8b, 0xff, 'g', 'z'}, 8) // non-UTF-8 proves byte-exact transfer

	rc, err := store.GetArtifact(ctx, "pkg/a", "abc123")
	if err != nil {
		t.Fatalf("GetArtifact(miss): %v", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("expected miss, got reader")
	}

	if err := store.PutArtifact(ctx, "pkg/a", "abc123", bytes.NewReader(entry)); err != nil {
		t.Fatalf("PutArtifact: %v", err)
	}
	rc, err = store.GetArtifact(ctx, "pkg/a", "abc123")
	if err != nil {
		t.Fatalf("GetArtifact(hit): %v", err)
	}
	if rc == nil {
		t.Fatal("expected hit after put, got miss")
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, entry) {
		t.Fatalf("restored %v, want %v", got, entry)
	}
}

func TestS3CacheBackendInactiveWithoutCreds(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("MAGUS_S3_BUCKET", "")
	store := s3Backend(t)
	if store.Active(context.Background()) {
		t.Fatal("Active() = true without credentials, want false")
	}
	rc, err := store.GetArtifact(context.Background(), "pkg/a", "abc123")
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("expected miss when not configured")
	}
}
