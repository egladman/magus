package bindings

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	buzzeng "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/internal/cache"
	ispell "github.com/egladman/magus/internal/spell"
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
	path := filepath.Join(t.TempDir(), "echo-import.buzz")
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
import "http" as xhttp

export fun mgs_getName() > str { return %q; }
export fun mgs_listTargets() > any {
    return {"get_artifact": {"fn": "get_artifact"}, "put_artifact": {"fn": "put_artifact"}};
}

final BASE = %q;

export fun get_artifact(target: any, cb: fun(any)) > bool {
    var io = {};
    cb(io);
    final url = BASE + "/blob/" + io["hash"];
    return xhttp.download(url, "" + io["dest"], {}) == 200;
}
export fun put_artifact(target: any, cb: fun(any)) > bool {
    var io = {};
    cb(io);
    final url = BASE + "/blob/" + io["hash"];
    final res = xhttp.upload_chunked("PUT", url, "" + io["src"], 0, {});
    return res[0] == 200;
}
`, name, srvURL)
	path := filepath.Join(t.TempDir(), name+".buzz")
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
	path := filepath.Join(t.TempDir(), "echo-spell.buzz")
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
	path := filepath.Join(repoRoot(t), "magus", "spells", "github", "actions", "spell.buzz")
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

// ghaEmulator emulates the GitHub Actions Cache service v2: the Twirp RPCs
// (CreateCacheEntry / FinalizeCacheEntryUpload / GetCacheEntryDownloadURL) plus
// the Azure-style blob PUT/GET the signed URLs point back at.
type ghaEmulator struct {
	mu        sync.Mutex
	pending   map[string][]byte // key -> uploaded bytes, awaiting finalize
	committed map[string][]byte // key -> finalized bytes
}

func newGHAEmulator() *ghaEmulator {
	return &ghaEmulator{
		pending:   map[string][]byte{},
		committed: map[string][]byte{},
	}
}

const ghaTwirp = "/twirp/github.actions.results.api.v1.CacheService/"

func (e *ghaEmulator) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == ghaTwirp+"CreateCacheEntry":
			e.createEntry(w, r)
		case r.Method == http.MethodPost && r.URL.Path == ghaTwirp+"FinalizeCacheEntryUpload":
			e.finalize(w, r)
		case r.Method == http.MethodPost && r.URL.Path == ghaTwirp+"GetCacheEntryDownloadURL":
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

func (e *ghaEmulator) createEntry(w http.ResponseWriter, r *http.Request) {
	var body struct{ Key, Version string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	e.mu.Lock()
	_, exists := e.committed[body.Key]
	e.mu.Unlock()
	if exists {
		// Already stored: v2 reports the conflict as ok=false, no upload URL.
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":              true,
		"signedUploadUrl": "http://" + r.Host + "/upload/" + body.Key,
	})
}

func (e *ghaEmulator) upload(w http.ResponseWriter, r *http.Request) {
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

func (e *ghaEmulator) finalize(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key       string
		SizeBytes string // int64 is a JSON string in proto3
		Version   string
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	e.mu.Lock()
	data, ok := e.pending[body.Key]
	e.mu.Unlock()
	if !ok {
		http.Error(w, "no pending upload for key", http.StatusBadRequest)
		return
	}
	if want, _ := strconv.ParseInt(body.SizeBytes, 10, 64); want != int64(len(data)) {
		http.Error(w, "size mismatch", http.StatusBadRequest)
		return
	}
	e.mu.Lock()
	e.committed[body.Key] = data
	delete(e.pending, body.Key)
	e.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "entryId": "1"})
}

func (e *ghaEmulator) downloadURL(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key         string
		RestoreKeys []string
		Version     string
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	e.mu.Lock()
	_, ok := e.committed[body.Key]
	e.mu.Unlock()
	if !ok {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":                true,
		"signedDownloadUrl": "http://" + r.Host + "/blob/" + body.Key,
		"matchedKey":        body.Key,
	})
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
	t.Setenv("ACTIONS_RESULTS_URL", srv.URL+"/")
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

// TestCanonicalTargetModule verifies the embedded "magus/target" source module
// imports through the normal host-module registration and its Target/Charm
// types resolve in both annotations and (nested) literals.
func TestCanonicalTargetModule(t *testing.T) {
	ctx := context.Background()
	sess := buzzeng.NewSession(ctx)
	defer sess.Close()
	registerHostModules(ctx, sess)

	src := `
import "magus/target";
fun build() > Target {
    return Target{
        name = "test",
        charms = [Charm{ name = "fast", enabled = true }],
        files = ["a.go"],
    };
}
export final tname = build().name;
export final cname = build().charms[0].name;
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("import \"magus/target\": %v", err)
	}
	exp := sess.Exports()
	if v, ok := exp["tname"]; !ok || !v.IsStr() || v.AsString() != "test" {
		t.Errorf("tname = %v, want \"test\"", v.String())
	}
	if v, ok := exp["cname"]; !ok || !v.IsStr() || v.AsString() != "fast" {
		t.Errorf("cname = %v, want \"fast\"", v.String())
	}
}

func TestNewCommandRenderer(t *testing.T) {
	targets := map[string]ispell.Target{
		"lint": {Cmd: "go", Args: []string{"tool", "golangci-lint", "run", "./..."}, Charms: map[string]ispell.Charm{
			"write": {Ops: []ispell.PatchOp{{Op: "add", Path: "/3", Value: "--fix"}}},
			"debug": {Ops: []ispell.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		}},
		"build": {Cmd: "go", Args: []string{"build"}},
		"fn":    {Func: "handler"}, // function-op: no static command
		"noop":  {},                // empty cmd
	}
	render := newCommandRenderer(targets)

	cases := []struct {
		name     string
		target   string
		charms   []string
		wantCmd  string
		wantArgs []string
		wantOK   bool
	}{
		{"base, no charms", "lint", nil, "go", []string{"tool", "golangci-lint", "run", "./..."}, true},
		{"charms applied", "lint", []string{"write", "debug"}, "go", []string{"tool", "golangci-lint", "run", "--fix", "./...", "-v"}, true},
		{"charmless target", "build", []string{"write"}, "go", []string{"build"}, true},
		{"function-op → no command", "fn", nil, "", nil, false},
		{"no-op (empty cmd) → none", "noop", nil, "", nil, false},
		{"unknown target → none", "missing", nil, "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, args, ok := render(tc.target, tc.charms)
			if ok != tc.wantOK || cmd != tc.wantCmd || !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("render(%q, %v) = (%q, %v, %v), want (%q, %v, %v)",
					tc.target, tc.charms, cmd, args, ok, tc.wantCmd, tc.wantArgs, tc.wantOK)
			}
		})
	}
}

func TestResolveCharmArgs(t *testing.T) {
	base := []string{"run", "./..."}
	// write inserts --fix before ./... (index 1); debug/trace append at the end.
	charmArgs := map[string]ispell.Charm{
		"write": {Ops: []ispell.PatchOp{{Op: "add", Path: "/1", Value: "--fix"}}},
		"debug": {Ops: []ispell.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		"trace": {Ops: []ispell.PatchOp{{Op: "add", Path: "/-", Value: "--trace"}}},
	}
	with := func(names ...string) context.Context {
		return types.WithCharms(context.Background(), names)
	}

	cases := []struct {
		name string
		ctx  context.Context
		want []string
	}{
		{"none active", context.Background(), []string{"run", "./..."}},
		{"append one", with("debug"), []string{"run", "./...", "-v"}},
		{"insert one", with("write"), []string{"run", "--fix", "./..."}},
		{"insert + append compose", with("write", "debug"), []string{"run", "--fix", "./...", "-v"}},
		{"appends sorted, order-independent", with("trace", "debug"), []string{"run", "./...", "-v", "--trace"}},
		{"duplicate active charm applied once", with("debug", "debug"), []string{"run", "./...", "-v"}},
		{"unknown charm ignored", with("nope"), []string{"run", "./..."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveCharmArgs(tc.ctx, base, charmArgs)
			if err != nil {
				t.Fatalf("resolveCharmArgs: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolveCharmArgs = %v, want %v", got, tc.want)
			}
		})
	}

	// The base slice must never be mutated.
	if !reflect.DeepEqual(base, []string{"run", "./..."}) {
		t.Errorf("base mutated: %v", base)
	}
	// nil charmArgs is a no-op.
	if got, err := resolveCharmArgs(with("write"), base, nil); err != nil || !reflect.DeepEqual(got, base) {
		t.Errorf("nil charmArgs: got %v, err %v, want %v", got, err, base)
	}
}

func TestDedupStrings(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{nil, nil},
		{[]string{"a"}, []string{"a"}},
		{[]string{"a", "b", "a"}, []string{"a", "b"}}, // manual + glob overlap
		{[]string{"go-build", "image-build", "go-build"}, []string{"go-build", "image-build"}},
		{[]string{"a", "a", "a"}, []string{"a"}},
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}}, // no dups: unchanged
	}
	for _, tc := range cases {
		got := dedupStrings(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("dedupStrings(%v) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("dedupStrings(%v) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

// These tests exercise the real spells/aws/s3-cache/spell.buzz against an emulator that
// independently recomputes the AWS SigV4 signature for every request and rejects
// a mismatch — the same check S3 performs. The signing-key chain is already
// verified against AWS's published vector in internal/std/extra/crypto; here we
// validate the spell's canonical-request and string-to-sign construction by
// cross-checking it with a second, independent (Go) implementation.

func s3Backend(t *testing.T) *spellRemoteBackend {
	t.Helper()
	path := filepath.Join(repoRoot(t), "magus", "spells", "aws", "s3-cache", "spell.buzz")
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
