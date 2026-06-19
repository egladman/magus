package bindings

import (
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blob deliberately includes NUL and 0xFF bytes — invalid UTF-8 — to prove the
// byte-level http primitives move bytes opaquely and never round-trip a payload
// through a rune-oriented Buzz string.
var blob = []byte{0x00, 0x01, 0xff, 0xfe, 'h', 'i', 0x00, 0x80, 0x7f, 0xff, 'b', 'y', 'e', 0x00}

func newHTTPBytesSession(t *testing.T) *buzz.Session {
	t.Helper()
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	sess.SetSyntheticModule("http", registerHTTPBytes())
	return sess
}

// callHTTPExport execs src, then invokes the exported function name with args.
func callHTTPExport(t *testing.T, sess *buzz.Session, src, name string, args ...buzz.Value) buzz.Value {
	t.Helper()
	require.NoError(t, sess.Exec(context.Background(), src), "Exec")
	fn, ok := sess.Exports()[name]
	require.True(t, ok, "export %q not found", name)
	v, err := sess.CallValue(context.Background(), fn, args)
	require.NoError(t, err, "call %q", name)
	return v
}

func TestDownloadStreamsBinaryToFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "yes", r.Header.Get("X-Test"), "header not forwarded")
		_, _ = w.Write(blob)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	src := `
import "http" as xhttp
export fun dl(url: str, dest: str) > int {
    return xhttp.download(url, dest, {"X-Test": "yes"});
}`
	got := callHTTPExport(t, newHTTPBytesSession(t), src, "dl", buzz.StrValue(srv.URL), buzz.StrValue(dest))
	require.True(t, got.IsInt(), "status not an int: %v", got)
	assert.Equal(t, int64(200), got.AsInt())
	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, blob, data)
}

func TestDownloadNon2xxWritesNoFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "missing.bin")
	src := `
import "http" as xhttp
export fun dl(url: str, dest: str) > int { return xhttp.download(url, dest, {}); }`
	got := callHTTPExport(t, newHTTPBytesSession(t), src, "dl", buzz.StrValue(srv.URL), buzz.StrValue(dest))
	assert.Equal(t, int64(204), got.AsInt())
	_, err := os.Stat(dest)
	assert.True(t, os.IsNotExist(err), "expected no file at %s, stat err = %v", dest, err)
}

func TestSizeReportsByteLength(t *testing.T) {
	p := filepath.Join(t.TempDir(), "blob.bin")
	require.NoError(t, os.WriteFile(p, blob, 0o644))
	src := `
import "http" as xhttp
export fun sz(p: str) > int { return xhttp.byteSize(p); }`
	got := callHTTPExport(t, newHTTPBytesSession(t), src, "sz", buzz.StrValue(p))
	assert.Equal(t, int64(len(blob)), got.AsInt())
}

func TestUploadChunkedReassembles(t *testing.T) {
	var mu sync.Mutex
	chunks := map[int64][]byte{}
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cr := r.Header.Get("Content-Range")
		var off, end, total int64
		_, err := fmt.Sscanf(cr, "bytes %d-%d/%d", &off, &end, &total)
		assert.NoError(t, err, "bad Content-Range %q", cr)
		mu.Lock()
		chunks[off] = body
		ranges = append(ranges, cr)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	srcFile := filepath.Join(t.TempDir(), "upload.bin")
	require.NoError(t, os.WriteFile(srcFile, blob, 0o644))

	// chunk_size 4 over a 14-byte blob forces 4 chunks (4+4+4+2).
	src := `
import "http" as xhttp
export fun up(url: str, src: str, chunk: int) > any {
    return xhttp.upload_chunked("PATCH", url, src, chunk, {});
}`
	got := callHTTPExport(t, newHTTPBytesSession(t), src, "up", buzz.StrValue(srv.URL), buzz.StrValue(srcFile), buzz.IntValue(4))
	require.True(t, got.IsList(), "upload return = %v, want [status, body]", got)
	require.Len(t, got.ListItems(), 2, "upload return = %v, want [status, body]", got)
	assert.Equal(t, int64(204), got.ListItems()[0].AsInt(), "final status")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, ranges, 4, "got chunks %v", ranges)
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
	assert.Equal(t, blob, got2, "reassembled")
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
	require.NoError(t, os.WriteFile(srcFile, blob, 0o644))
	src := `
import "http" as xhttp
export fun up(url: str, src: str) > any {
    return xhttp.upload_chunked("PUT", url, src, 0, {});
}`
	got := callHTTPExport(t, newHTTPBytesSession(t), src, "up", buzz.StrValue(srv.URL), buzz.StrValue(srcFile))
	assert.Equal(t, int64(200), got.ListItems()[0].AsInt(), "status")
	assert.Empty(t, gotRange, "single-shot upload sent Content-Range %q, want none", gotRange)
	assert.Equal(t, blob, gotBody, "uploaded bytes")
}
