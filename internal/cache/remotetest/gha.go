// Package remotetest serves the remote cache protocols magus's backend spells speak,
// so a test can drive a real spell handler end to end without the real service.
package remotetest

import (
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	json "github.com/egladman/magus/internal/json"
)

// GHA emulates the GitHub Actions Cache service v2 that spells/github/actions calls:
// the Twirp RPCs (CreateCacheEntry, FinalizeCacheEntryUpload, GetCacheEntryDownloadURL)
// plus the Azure-style blob PUT and GET its signed URLs point back at. Set the option
// fields before serving; it is safe for concurrent requests.
type GHA struct {
	// CamelCase answers in lowerCamel JSON names. The service answers in protobuf names
	// (signed_upload_url); the official toolkit's decoder accepts both, so the spell does too.
	CamelCase bool
	// RefuseCreate answers CreateCacheEntry with ok=false: another job holds the reservation.
	RefuseCreate bool
	// FailStatus, when set, is every Twirp call's answer: a service that is down.
	FailStatus int

	mu        sync.Mutex
	pending   map[string][]byte
	committed map[string][]byte
	twirpAuth []string
}

// NewGHA returns an emulator holding no entries.
func NewGHA() *GHA {
	return &GHA{pending: map[string][]byte{}, committed: map[string][]byte{}}
}

// Serve starts e for the rest of t and points the spell at it through
// ACTIONS_RESULTS_URL and ACTIONS_RUNTIME_TOKEN, the variables an Actions step exports.
func (e *GHA) Serve(t *testing.T, token string) {
	t.Helper()
	srv := httptest.NewServer(e.Handler())
	t.Cleanup(srv.Close)
	t.Setenv("ACTIONS_RESULTS_URL", srv.URL+"/")
	t.Setenv("ACTIONS_RUNTIME_TOKEN", token)
}

// TwirpAuths returns the Authorization header each Twirp call arrived with, in order.
func (e *GHA) TwirpAuths() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.twirpAuth...)
}

// Committed returns a copy of the finalized entries, keyed as the spell stored them.
func (e *GHA) Committed() map[string][]byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.committed)
}

const twirp = "/twirp/github.actions.results.api.v1.CacheService/"

// versionHex is the only `version` the real service accepts; it answered the old salt
// "magus-remote-v1" with the 400 below, in CI on 2026-09-10 (run 34550323236).
var versionHex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Handler serves the service's routes.
func (e *GHA) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The spell declares a magus\secret grant rather than building the header, so
		// recording it is what lets a test prove the credential reached the wire. The
		// pre-signed blob URLs carry no auth: they are another host in production, and
		// a grant must not follow them.
		if strings.HasPrefix(r.URL.Path, twirp) {
			e.mu.Lock()
			e.twirpAuth = append(e.twirpAuth, r.Header.Get("Authorization"))
			e.mu.Unlock()
			if e.FailStatus != 0 {
				http.Error(w, "unavailable", e.FailStatus)
				return
			}
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == twirp+"CreateCacheEntry":
			e.createEntry(w, r)
		case r.Method == http.MethodPost && r.URL.Path == twirp+"FinalizeCacheEntryUpload":
			e.finalize(w, r)
		case r.Method == http.MethodPost && r.URL.Path == twirp+"GetCacheEntryDownloadURL":
			e.downloadURL(w, r)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/upload/"):
			e.upload(w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/blob/"):
			e.blob(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (e *GHA) urlField(proto, camel string) string {
	if e.CamelCase {
		return camel
	}
	return proto
}

// rejectVersion answers a non-digest version the way the service does, and reports
// whether it wrote that response.
func rejectVersion(w http.ResponseWriter, version string) bool {
	if versionHex.MatchString(version) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = io.WriteString(w, `{"code":"invalid_argument","msg":"version invalid length for cache entry version: must be between 1 and 64 characters","meta":{"argument":"version"}}`)
	return true
}

func (e *GHA) createEntry(w http.ResponseWriter, r *http.Request) {
	// Explicit tags: under GOEXPERIMENT=jsonv2 field matching is case-sensitive, so an
	// untagged Key never sees the wire's "key".
	var body struct {
		Key     string `json:"key"`
		Version string `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	if rejectVersion(w, body.Version) {
		return
	}
	e.mu.Lock()
	_, exists := e.committed[body.Key]
	e.mu.Unlock()
	if exists {
		// Twirp's already_exists, which the toolkit reports as "cache already exists".
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"code":"already_exists","msg":"cache entry already exists"}`)
		return
	}
	if e.RefuseCreate {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "message": "reservation refused"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true,
		e.urlField("signed_upload_url", "signedUploadUrl"): "http://" + r.Host + "/upload/" + body.Key,
	})
}

func (e *GHA) upload(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("x-ms-blob-type"); got != "BlockBlob" {
		http.Error(w, "x-ms-blob-type="+got, http.StatusBadRequest)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/upload/")
	body, _ := io.ReadAll(r.Body)
	e.mu.Lock()
	e.pending[key] = body
	e.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
}

func (e *GHA) finalize(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key       string `json:"key"`
		SizeBytes string `json:"size_bytes"` // int64 is a JSON string in proto3
		Version   string `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if rejectVersion(w, body.Version) {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	data, ok := e.pending[body.Key]
	if !ok {
		http.Error(w, "no pending upload for key", http.StatusBadRequest)
		return
	}
	if want, _ := strconv.ParseInt(body.SizeBytes, 10, 64); want != int64(len(data)) {
		http.Error(w, "size mismatch", http.StatusBadRequest)
		return
	}
	e.committed[body.Key] = data
	delete(e.pending, body.Key)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "entryId": "1"})
}

func (e *GHA) downloadURL(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key         string   `json:"key"`
		RestoreKeys []string `json:"restore_keys"`
		Version     string   `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if rejectVersion(w, body.Version) {
		return
	}
	e.mu.Lock()
	_, ok := e.committed[body.Key]
	e.mu.Unlock()
	if !ok {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true,
		e.urlField("signed_download_url", "signedDownloadUrl"): "http://" + r.Host + "/blob/" + body.Key,
		e.urlField("matched_key", "matchedKey"):                body.Key,
	})
}

func (e *GHA) blob(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/blob/")
	e.mu.Lock()
	data, ok := e.committed[key]
	e.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = w.Write(data)
}
