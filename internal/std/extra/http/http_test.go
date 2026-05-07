package extrahttp_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	extrahttp "github.com/egladman/magus/internal/std/extra/http"
)

// blob deliberately includes NUL and 0xFF bytes — invalid UTF-8 — to prove the
// primitives move bytes opaquely and never round-trip a payload through a
// rune-oriented Buzz string.
var blob = []byte{0x00, 0x01, 0xff, 0xfe, 'h', 'i', 0x00, 0x80, 0x7f, 0xff, 'b', 'y', 'e', 0x00}

func newSession(t *testing.T) *buzz.Session {
	t.Helper()
	sess := buzz.NewSession(context.Background())
	sess.SetSyntheticModule("magus/extra/http", extrahttp.Register(context.Background(), sess))
	return sess
}

// callExport execs src, then invokes the exported function name with args.
func callExport(t *testing.T, sess *buzz.Session, src, name string, args ...buzz.Value) buzz.Value {
	t.Helper()
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	fn, ok := sess.Exports()[name]
	if !ok {
		t.Fatalf("export %q not found", name)
	}
	v, err := sess.CallValue(context.Background(), fn, args)
	if err != nil {
		t.Fatalf("call %q: %v", name, err)
	}
	return v
}

func TestDownloadStreamsBinaryToFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "yes" {
			t.Errorf("header not forwarded: %q", r.Header.Get("X-Test"))
		}
		_, _ = w.Write(blob)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	src := `
import "magus/extra/http" as xhttp
export fun dl(url: str, dest: str) > int {
    return xhttp.download(url, dest, {"X-Test": "yes"});
}`
	got := callExport(t, newSession(t), src, "dl", buzz.StrValue(srv.URL), buzz.StrValue(dest))
	if !got.IsInt() || got.AsInt() != 200 {
		t.Fatalf("status = %v, want 200", got)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, blob) {
		t.Fatalf("downloaded bytes = %v, want %v", data, blob)
	}
}

func TestDownloadNon2xxWritesNoFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "missing.bin")
	src := `
import "magus/extra/http" as xhttp
export fun dl(url: str, dest: str) > int { return xhttp.download(url, dest, {}); }`
	got := callExport(t, newSession(t), src, "dl", buzz.StrValue(srv.URL), buzz.StrValue(dest))
	if got.AsInt() != 204 {
		t.Fatalf("status = %v, want 204", got)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected no file at %s, stat err = %v", dest, err)
	}
}

func TestSizeReportsByteLength(t *testing.T) {
	p := filepath.Join(t.TempDir(), "blob.bin")
	if err := os.WriteFile(p, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	src := `
import "magus/extra/http" as xhttp
export fun sz(p: str) > int { return xhttp.byteSize(p); }`
	got := callExport(t, newSession(t), src, "sz", buzz.StrValue(p))
	if got.AsInt() != int64(len(blob)) {
		t.Fatalf("size = %d, want %d", got.AsInt(), len(blob))
	}
}

func TestUploadChunkedReassembles(t *testing.T) {
	var mu sync.Mutex
	chunks := map[int64][]byte{}
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cr := r.Header.Get("Content-Range")
		var off, end, total int64
		if _, err := fmt.Sscanf(cr, "bytes %d-%d/%d", &off, &end, &total); err != nil {
			t.Errorf("bad Content-Range %q: %v", cr, err)
		}
		mu.Lock()
		chunks[off] = body
		ranges = append(ranges, cr)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	srcFile := filepath.Join(t.TempDir(), "upload.bin")
	if err := os.WriteFile(srcFile, blob, 0o644); err != nil {
		t.Fatal(err)
	}

	// chunk_size 4 over a 14-byte blob forces 4 chunks (4+4+4+2).
	src := `
import "magus/extra/http" as xhttp
export fun up(url: str, src: str, chunk: int) > any {
    return xhttp.upload_chunked("PATCH", url, src, chunk, {});
}`
	got := callExport(t, newSession(t), src, "up", buzz.StrValue(srv.URL), buzz.StrValue(srcFile), buzz.IntValue(4))
	if !got.IsList() || len(got.ListItems()) != 2 {
		t.Fatalf("upload return = %v, want [status, body]", got)
	}
	if status := got.ListItems()[0].AsInt(); status != 204 {
		t.Fatalf("final status = %d, want 204", status)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(ranges) != 4 {
		t.Fatalf("got %d chunks (%v), want 4", len(ranges), ranges)
	}
	// Reassemble in offset order and compare to the original.
	offsets := make([]int64, 0, len(chunks))
	for off := range chunks {
		offsets = append(offsets, off)
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	var got2 []byte
	for _, off := range offsets {
		got2 = append(got2, chunks[off]...)
	}
	if !bytes.Equal(got2, blob) {
		t.Fatalf("reassembled = %v, want %v", got2, blob)
	}
}

func TestUploadSingleShotNoContentRange(t *testing.T) {
	var gotRange string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Content-Range")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	srcFile := filepath.Join(t.TempDir(), "upload.bin")
	if err := os.WriteFile(srcFile, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	src := `
import "magus/extra/http" as xhttp
export fun up(url: str, src: str) > any {
    return xhttp.upload_chunked("PUT", url, src, 0, {});
}`
	got := callExport(t, newSession(t), src, "up", buzz.StrValue(srv.URL), buzz.StrValue(srcFile))
	if got.ListItems()[0].AsInt() != 200 {
		t.Fatalf("status = %d, want 200", got.ListItems()[0].AsInt())
	}
	if gotRange != "" {
		t.Fatalf("single-shot upload sent Content-Range %q, want none", gotRange)
	}
	if !bytes.Equal(gotBody, blob) {
		t.Fatalf("uploaded bytes = %v, want %v", gotBody, blob)
	}
}
