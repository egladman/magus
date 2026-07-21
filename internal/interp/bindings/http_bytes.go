package bindings

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// maxChunk caps a single upload chunk; callers may request less but not more.
// 32 MiB matches the GitHub Actions Cache per-PATCH ceiling, the tightest of the
// providers this module targets.
const maxChunk = 32 * 1024 * 1024

// registerHTTPBytes builds the byte-level companion to the declarative http
// module: streaming a response body to a file, reading a file's byte length, and
// uploading a file in Content-Range chunks. They can't be declarative std.Methods
// (Buzz strings are rune-oriented: len() counts runes, strings can't be
// byte-indexed), so a magusfile cannot compute a binary blob's byte size or slice
// it into upload chunks on its own. These keep the bytes in Go while leaving all
// protocol decisions — URLs, headers, auth, chunk size — to the calling Buzz
// script. That split is what lets a remote cache backend (e.g. GitHub Actions
// Cache) be written entirely in Buzz with no provider-specific Go code. This is
// VM glue, hand-written against the gopherbuzz value API and merged onto the
// generated `http` module map at bind time (see registerMagusModules); it lives
// here, not on the VM-agnostic std surface.
func registerHTTPBytes() vm.Value {
	m := vm.NewMap()

	// download(url, dest, headers?) -> int
	// GET url, streaming the response body to dest (created/truncated). Returns
	// the HTTP status code; the body is never materialised as a Buzz string, so
	// arbitrary binary survives intact. A non-2xx status writes no file.
	m.MapSet("download", vm.DirectValue("http.download", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		url := httpStrArg(args, 0)
		dest := httpStrArg(args, 1)
		headers := httpMapArg(args, 2)
		status, err := httpDownload(ctx, url, dest, headers)
		if err != nil {
			return vm.Null, err
		}
		return vm.IntValue(int64(status)), nil
	}))

	// byteSize(path) -> int
	// Byte length of the file at path. The companion to upload_chunked: a script
	// needs the true byte count for a Content-Range total or a commit "size",
	// which len() on a Buzz string cannot give for binary data. Named byteSize
	// (not size) because a module map's built-in .size() method — its entry count —
	// shadows a stored key of the same name.
	m.MapSet("byteSize", vm.DirectValue("http.byteSize", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		fi, err := os.Stat(httpStrArg(args, 0))
		if err != nil {
			return vm.Null, fmt.Errorf("http.byteSize: %w", err)
		}
		return vm.IntValue(fi.Size()), nil
	}))

	// upload_chunked(method, url, src, chunk_size, headers?) -> [int, str]
	// Send the file at src as the request body using method. When chunk_size > 0
	// the file is sent in chunk_size-byte slices (capped at 32 MiB), each carrying
	// a `Content-Range: bytes a-b/total` header — the resumable-upload convention
	// GitHub Actions Cache (and RFC 7233 servers) expect. chunk_size <= 0 sends
	// the whole file in one request with no Content-Range. Returns the final
	// [status, body]; body is small (servers ack chunks with empty/JSON bodies).
	m.MapSet("upload_chunked", vm.DirectValue("http.upload_chunked", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		method := httpStrArg(args, 0)
		url := httpStrArg(args, 1)
		src := httpStrArg(args, 2)
		chunk := httpIntArg(args, 3)
		headers := httpMapArg(args, 4)
		status, body, err := httpUploadChunked(ctx, method, url, src, chunk, headers)
		if err != nil {
			return vm.Null, err
		}
		return vm.ListValue([]vm.Value{vm.IntValue(int64(status)), vm.StrValue(body)}), nil
	}))

	return m
}

func httpDownload(ctx context.Context, url, dest string, headers map[string]string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("http.download: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("http.download %s: %w", url, err)
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
		return resp.StatusCode, fmt.Errorf("http.download: create: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return resp.StatusCode, fmt.Errorf("http.download: write: %w", err)
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

func httpUploadChunked(ctx context.Context, method, url, src string, chunkSize int64, headers map[string]string) (int, string, error) {
	f, err := os.Open(src)
	if err != nil {
		return 0, "", fmt.Errorf("http.upload_chunked: open: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, "", fmt.Errorf("http.upload_chunked: stat: %w", err)
	}
	total := fi.Size()

	// Single-shot: whole file as the body, no Content-Range.
	if chunkSize <= 0 {
		return httpSendChunk(ctx, method, url, f, total, headers, "")
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
		status, body, err = httpSendChunk(ctx, method, url, section, end-offset, headers, rng)
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

func httpSendChunk(ctx context.Context, method, url string, body io.Reader, length int64, headers map[string]string, contentRange string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, "", fmt.Errorf("http.upload_chunked: request: %w", err)
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
		return 0, "", fmt.Errorf("http.upload_chunked %s: %w", url, err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return resp.StatusCode, string(rb), nil
}

func httpStrArg(args []vm.Value, i int) string {
	if i < len(args) && args[i].IsStr() {
		return args[i].AsString()
	}
	return ""
}

func httpIntArg(args []vm.Value, i int) int64 {
	if i < len(args) && args[i].IsInt() {
		return args[i].AsInt()
	}
	return 0
}

func httpMapArg(args []vm.Value, i int) map[string]string {
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
