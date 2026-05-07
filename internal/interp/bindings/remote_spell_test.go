package bindings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// TestImportedBuzzSpellHasFunctionOps is the generalization check: a Buzz spell
// loaded via the ordinary import path (loadLocalBuzzSpell) — not loadSpellFile or
// magus.cache.remote — registers with function-op support, so any imported Buzz
// spell can carry function-ops, not only cache backends.
func TestImportedBuzzSpellHasFunctionOps(t *testing.T) {
	src := `
export fun mgs_getName() > str { return "echo-import"; }
export fun mgs_listTargets() > any { return {"echo": {"fn": "echo"}}; }
export fun echo(target: any, cb: fun(any)) > str { var p = {}; cb(p); return "yo " + p["x"]; }
`
	path := filepath.Join(t.TempDir(), "echo-import.bzz")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	metadata, ok := loadLocalBuzzSpell(context.Background(), path)
	if !ok || metadata.Name != "echo-import" {
		t.Fatalf("loadLocalBuzzSpell ok=%v name=%q", ok, metadata.Name)
	}
	drv, found := project.DefaultSpellRegistry().Lookup("echo-import")
	if !found {
		t.Fatal("import path did not register the spell")
	}
	resp, err := drv.Invoke(context.Background(), types.InvokeRequest{
		Target: "echo",
		Params: map[string]any{"x": "there"},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp.Data != "yo there" {
		t.Fatalf("function-op via import path: Data = %v, want %q", resp.Data, "yo there")
	}
}

// --- generic spell-backed RemoteBackend round trip ---------------------------

// blobStore stands in for a remote cache provider: PUT /blob/<hash> stores a
// body, GET /blob/<hash> serves it (404 on miss).
type blobStore struct {
	mu    sync.Mutex
	items map[string][]byte
}

func (s *blobStore) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hash := filepath.Base(r.URL.Path)
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			body, ok := s.items[hash]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(body)
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			s.items[hash] = body
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

// writeSpell writes a minimal Buzz cache-backend spell (real mgs_ functions with
// function-ops) targeting srvURL, named uniquely so loads don't collide.
func writeSpell(t *testing.T, name, srvURL string) string {
	t.Helper()
	src := fmt.Sprintf(`
import "magus/extra/http" as xhttp

export fun mgs_getName() > str { return %q; }
export fun mgs_listTargets() > any {
    return {"get_artifact": {"fn": "get_artifact"}, "put_artifact": {"fn": "put_artifact"}};
}

const BASE = %q;

export fun get_artifact(target: any, cb: fun(any)) > bool {
    var io = {};
    cb(io);
    const url = BASE + "/blob/" + io["hash"];
    return xhttp.download(url, "" + io["dest"], {}) == 200;
}
export fun put_artifact(target: any, cb: fun(any)) > bool {
    var io = {};
    cb(io);
    const url = BASE + "/blob/" + io["hash"];
    const res = xhttp.upload_chunked("PUT", url, "" + io["src"], 0, {});
    return res[0] == 200;
}
`, name, srvURL)
	path := filepath.Join(t.TempDir(), name+".bzz")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSpellRemoteBackendRoundTrip(t *testing.T) {
	store := &blobStore{items: map[string][]byte{}}
	srv := httptest.NewServer(store.handler())
	defer srv.Close()

	drv, err := resolveBackendSpell(context.Background(), writeSpell(t, "test-cache-rt", srv.URL))
	if err != nil {
		t.Fatalf("resolveBackendSpell: %v", err)
	}
	rs := &spellRemoteBackend{drv: drv}
	ctx := context.Background()
	// Non-UTF-8 bytes prove the path moves bytes, not text.
	entry := []byte{0x1f, 0x8b, 0x00, 0xff, 'g', 'z', 0x00, 0x7f, 0x80}

	rc, err := rs.GetArtifact(ctx, "pkg/a", "deadbeef")
	if err != nil {
		t.Fatalf("GetArtifact(miss): %v", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("GetArtifact(miss): expected nil reader")
	}

	if err := rs.PutArtifact(ctx, "pkg/a", "deadbeef", bytes.NewReader(entry)); err != nil {
		t.Fatalf("PutArtifact: %v", err)
	}
	store.mu.Lock()
	stored, ok := store.items["deadbeef"]
	store.mu.Unlock()
	if !ok || !bytes.Equal(stored, entry) {
		t.Fatalf("server stored %v, want %v", stored, entry)
	}

	rc, err = rs.GetArtifact(ctx, "pkg/a", "deadbeef")
	if err != nil {
		t.Fatalf("GetArtifact(hit): %v", err)
	}
	if rc == nil {
		t.Fatal("GetArtifact(hit): expected reader, got nil")
	}
	got, _ := io.ReadAll(rc)
	tmpPath := rc.(*removeOnClose).path
	if err := rc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !bytes.Equal(got, entry) {
		t.Fatalf("restored %v, want %v", got, entry)
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("temp file %s not removed on Close (stat err %v)", tmpPath, err)
	}
}

func TestResolveBackendSpellMissingName(t *testing.T) {
	if _, err := resolveBackendSpell(context.Background(), "no-such-spell-xyz"); err == nil {
		t.Fatal("expected error for unregistered spell name")
	}
}

// TestLoadSpellFileFunctionOp checks the function-op machinery directly: a loaded
// spell's op runs in the VM, receives Params, and returns Data.
func TestLoadSpellFileFunctionOp(t *testing.T) {
	src := `
export fun mgs_getName() > str { return "echo-spell"; }
export fun mgs_listTargets() > any { return {"echo": {"fn": "echo"}}; }
export fun echo(target: any, cb: fun(any)) > str { var p = {}; cb(p); return "hi " + p["who"]; }
`
	path := filepath.Join(t.TempDir(), "echo-spell.bzz")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err := loadSpellFile(context.Background(), path)
	if err != nil {
		t.Fatalf("loadSpellFile: %v", err)
	}
	resp, err := sp.Invoke(context.Background(), types.InvokeRequest{
		Target: "echo",
		Params: map[string]any{"who": "magus"},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp.Data != "hi magus" {
		t.Fatalf("Data = %v, want %q", resp.Data, "hi magus")
	}
}

// --- real the github spell against an emulated GitHub Actions Cache API -------

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root (go.mod) not found")
		}
		dir = parent
	}
}

func ghaBackend(t *testing.T) *spellRemoteBackend {
	t.Helper()
	path := filepath.Join(repoRoot(t), "magus", "spells", "github", "actions", "spell.bzz")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("github spell not found at %s: %v", path, err)
	}
	drv, err := resolveBackendSpell(context.Background(), path)
	if err != nil {
		t.Fatalf("load github spell: %v", err)
	}
	if drv.Name() != "actions" {
		t.Fatalf("spell name = %q, want actions", drv.Name())
	}
	return &spellRemoteBackend{drv: drv}
}

type ghaEmulator struct {
	mu        sync.Mutex
	nextID    int64
	idToKey   map[int64]string
	uploads   map[int64][]byte
	committed map[string][]byte
}

func newGHAEmulator() *ghaEmulator {
	return &ghaEmulator{
		idToKey:   map[int64]string{},
		uploads:   map[int64][]byte{},
		committed: map[string][]byte{},
	}
}

func (e *ghaEmulator) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/_apis/artifactcache/cache"):
			e.lookup(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/_apis/artifactcache/caches":
			e.reserve(w, r)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/_apis/artifactcache/caches/"):
			e.upload(w, r)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/_apis/artifactcache/caches/"):
			e.commit(w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/blob/"):
			e.blob(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (e *ghaEmulator) lookup(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("keys")
	e.mu.Lock()
	_, ok := e.committed[key]
	e.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"archiveLocation": "http://" + r.Host + "/blob/" + key,
	})
}

func (e *ghaEmulator) reserve(w http.ResponseWriter, r *http.Request) {
	var body struct{ Key, Version string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	e.mu.Lock()
	e.nextID++
	id := e.nextID
	e.idToKey[id] = body.Key
	e.uploads[id] = nil
	e.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]int64{"cacheId": id})
}

func (e *ghaEmulator) cacheID(path string) int64 {
	var id int64
	_, _ = fmt.Sscanf(filepath.Base(path), "%d", &id)
	return id
}

func (e *ghaEmulator) upload(w http.ResponseWriter, r *http.Request) {
	id := e.cacheID(r.URL.Path)
	chunk, _ := io.ReadAll(r.Body)
	var off, end, total int64
	if _, err := fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/%d", &off, &end, &total); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	e.mu.Lock()
	buf := e.uploads[id]
	if int64(len(buf)) < total {
		grown := make([]byte, total)
		copy(grown, buf)
		buf = grown
	}
	copy(buf[off:], chunk)
	e.uploads[id] = buf
	e.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (e *ghaEmulator) commit(w http.ResponseWriter, r *http.Request) {
	id := e.cacheID(r.URL.Path)
	var body struct{ Size int64 }
	_ = json.NewDecoder(r.Body).Decode(&body)
	e.mu.Lock()
	key := e.idToKey[id]
	data := e.uploads[id]
	if body.Size != int64(len(data)) {
		e.mu.Unlock()
		http.Error(w, "size mismatch", http.StatusBadRequest)
		return
	}
	e.committed[key] = data
	e.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (e *ghaEmulator) blob(w http.ResponseWriter, r *http.Request) {
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

func TestGHACacheBackendRoundTrip(t *testing.T) {
	emu := newGHAEmulator()
	srv := httptest.NewServer(emu.handler())
	defer srv.Close()

	t.Setenv("GITHUB_ACTIONS", "true") // enabled() gate
	t.Setenv("ACTIONS_CACHE_URL", srv.URL+"/")
	t.Setenv("ACTIONS_RUNTIME_TOKEN", "test-token")

	store := ghaBackend(t)
	if !store.Active(context.Background()) {
		t.Fatal("Active() = false under GITHUB_ACTIONS=true, want true")
	}
	ctx := context.Background()
	entry := bytes.Repeat([]byte{0x00, 0x1f, 0x8b, 0xff}, 10)

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

// Outside GitHub Actions the spell's enabled() op returns false, so the backend
// reports inactive and the cache skips it entirely — no remote calls.
func TestGHACacheBackendInactiveOutsideGHA(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	store := ghaBackend(t)
	if store.Active(context.Background()) {
		t.Fatal("Active() = true outside GitHub Actions, want false")
	}
	rc, err := store.GetArtifact(context.Background(), "pkg/a", "abc123")
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("expected miss when not running under GitHub Actions")
	}
}
