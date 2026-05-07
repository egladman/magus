// Package extrahttp is an optional Buzz module ("magus/extra/http") that adds
// the byte-level HTTP primitives the core std/http module deliberately omits:
// streaming a response body to a file, reading a file's byte length, and
// uploading a file in Content-Range chunks.
//
// It exists because Buzz strings are rune-oriented (len() counts runes, strings
// can't be byte-indexed), so a magusfile cannot compute a binary blob's byte
// size or slice it into upload chunks on its own. These three primitives keep
// the bytes in Go while leaving all protocol decisions — URLs, headers, auth,
// chunk size — to the calling Buzz script. That split is what lets a remote
// cache backend (e.g. GitHub Actions Cache) be written entirely in Buzz with no
// provider-specific Go code: see the cache RemoteBackend bridge that imports this.
//
// The module is hand-written against the public magus/gopherbuzz value API rather than
// generated, matching the existing buzz binding convention (one VM backend, so
// codegen earns nothing here).
package extrahttp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	buzz "github.com/egladman/gopherbuzz"
)

// maxChunk caps a single upload chunk; callers may request less but not more.
// 32 MiB matches the GitHub Actions Cache per-PATCH ceiling, the tightest of the
// providers this module targets.
const maxChunk = 32 * 1024 * 1024

// Register builds the "magus/extra/http" module map. The host installs it with
// sess.SetSyntheticModule("magus/extra/http", Register(ctx, sess)) so a script
// reaches it via `import "magus/extra/http"`.
func Register(_ context.Context, _ *buzz.Session) buzz.Value {
	m := buzz.NewMap()

	// download(url, dest, headers?) -> int
	// GET url, streaming the response body to dest (created/truncated). Returns
	// the HTTP status code; the body is never materialised as a Buzz string, so
	// arbitrary binary survives intact. A non-2xx status writes no file.
	m.MapSet("download", buzz.DirectValue("extra/http.download", func(ctx context.Context, args []buzz.Value) (buzz.Value, error) {
		url := strArg(args, 0)
		dest := strArg(args, 1)
		headers := mapArg(args, 2)
		status, err := download(ctx, url, dest, headers)
		if err != nil {
			return buzz.Null, err
		}
		return buzz.IntValue(int64(status)), nil
	}))

	// byteSize(path) -> int
	// Byte length of the file at path. The companion to upload_chunked: a script
	// needs the true byte count for a Content-Range total or a commit "size",
	// which len() on a Buzz string cannot give for binary data. Named byteSize
	// (not size) because a module map's built-in .size() method — its entry count —
	// shadows a stored key of the same name.
	m.MapSet("byteSize", buzz.DirectValue("extra/http.byteSize", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		fi, err := os.Stat(strArg(args, 0))
		if err != nil {
			return buzz.Null, fmt.Errorf("extra/http.byteSize: %w", err)
		}
		return buzz.IntValue(fi.Size()), nil
	}))

	// upload_chunked(method, url, src, chunk_size, headers?) -> [int, str]
	// Send the file at src as the request body using method. When chunk_size > 0
	// the file is sent in chunk_size-byte slices (capped at 32 MiB), each carrying
	// a `Content-Range: bytes a-b/total` header — the resumable-upload convention
	// GitHub Actions Cache (and RFC 7233 servers) expect. chunk_size <= 0 sends
	// the whole file in one request with no Content-Range. Returns the final
	// [status, body]; body is small (servers ack chunks with empty/JSON bodies).
	m.MapSet("upload_chunked", buzz.DirectValue("extra/http.upload_chunked", func(ctx context.Context, args []buzz.Value) (buzz.Value, error) {
		method := strArg(args, 0)
		url := strArg(args, 1)
		src := strArg(args, 2)
		chunk := intArg(args, 3)
		headers := mapArg(args, 4)
		status, body, err := uploadChunked(ctx, method, url, src, chunk, headers)
		if err != nil {
			return buzz.Null, err
		}
		return buzz.ListValue([]buzz.Value{buzz.IntValue(int64(status)), buzz.StrValue(body)}), nil
	}))

	return m
}

func download(ctx context.Context, url, dest string, headers map[string]string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("extra/http.download: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("extra/http.download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Only a 200 carries a full body worth persisting; for anything else
		// (204 miss, 4xx, redirects) write nothing and let the caller branch on
		// the returned status. Avoids leaving stray empty files on a miss.
		return resp.StatusCode, nil
	}

	// Write atomically: temp + rename, so a reader never sees a partial file.
	tmp := dest + ".dl.tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("extra/http.download: create: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return resp.StatusCode, fmt.Errorf("extra/http.download: write: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return resp.StatusCode, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

func uploadChunked(ctx context.Context, method, url, src string, chunkSize int64, headers map[string]string) (int, string, error) {
	f, err := os.Open(src)
	if err != nil {
		return 0, "", fmt.Errorf("extra/http.upload_chunked: open: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, "", fmt.Errorf("extra/http.upload_chunked: stat: %w", err)
	}
	total := fi.Size()

	// Single-shot: whole file as the body, no Content-Range.
	if chunkSize <= 0 {
		return sendChunk(ctx, method, url, f, total, headers, "")
	}
	if chunkSize > maxChunk {
		chunkSize = maxChunk
	}

	var status int
	var body string
	for offset := int64(0); offset < total; offset += chunkSize {
		end := offset + chunkSize
		if end > total {
			end = total
		}
		section := io.NewSectionReader(f, offset, end-offset)
		rng := fmt.Sprintf("bytes %d-%d/%d", offset, end-1, total)
		var err error
		status, body, err = sendChunk(ctx, method, url, section, end-offset, headers, rng)
		if err != nil {
			return status, body, err
		}
		// Stop at the first chunk the server rejects rather than uploading the
		// rest against an entry it has already refused; return its status so the
		// caller branches. Not a Go error — a 4xx/5xx is a server decision.
		if status < 200 || status >= 300 {
			return status, body, nil
		}
	}
	return status, body, nil
}

func sendChunk(ctx context.Context, method, url string, body io.Reader, length int64, headers map[string]string, contentRange string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, "", fmt.Errorf("extra/http.upload_chunked: request: %w", err)
	}
	req.ContentLength = length
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if contentRange != "" {
		req.Header.Set("Content-Range", contentRange)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("extra/http.upload_chunked %s: %w", url, err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return resp.StatusCode, string(rb), nil
}

func strArg(args []buzz.Value, i int) string {
	if i < len(args) && args[i].IsStr() {
		return args[i].AsString()
	}
	return ""
}

func intArg(args []buzz.Value, i int) int64 {
	if i < len(args) && args[i].IsInt() {
		return args[i].AsInt()
	}
	return 0
}

func mapArg(args []buzz.Value, i int) map[string]string {
	if i >= len(args) || !args[i].IsMap() {
		return nil
	}
	out := map[string]string{}
	for _, k := range args[i].MapKeys() {
		if v, ok := args[i].MapGet(k); ok && v.IsStr() {
			out[k] = v.AsString()
		}
	}
	return out
}
