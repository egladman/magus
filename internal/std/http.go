package std

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/egladman/magus/internal/retry"
	"github.com/egladman/magus/internal/sandbox"
)

//go:generate go run ../../cmd/magus-bindings-gen -module http -lang lua -out gen/lua/http.go
//go:generate go run ../../cmd/magus-bindings-gen -module http -lang buzz -out gen/buzz/http.go

func init() { Register(HTTP) }

// HTTP is the "http" host module: an HTTP client with automatic retry on transient errors.
var HTTP = Module{
	Name: "http",
	Doc:  "HTTP client with automatic retry on transient errors.",
	Methods: []Method{
		{
			Name: "get",
			Doc:  "Send a GET request; returns (status_code, body).",
			Args: []Arg{
				{Name: "url", Type: TypeString},
				{Name: "headers", Type: TypeStringMap, Optional: true},
			},
			Returns: []Ret{{Name: "status", Type: TypeInt}, {Name: "body", Type: TypeString}},
			Impl:    HTTPGet,
		},
		{
			Name: "post",
			Doc:  "Send a POST request with body; returns (status_code, body).",
			Args: []Arg{
				{Name: "url", Type: TypeString},
				{Name: "body", Type: TypeString},
				{Name: "headers", Type: TypeStringMap, Optional: true},
			},
			Returns: []Ret{{Name: "status", Type: TypeInt}, {Name: "body", Type: TypeString}},
			Impl:    HTTPPost,
		},
		{
			Name: "request",
			Doc:  "Send an HTTP request; returns (status_code, body).",
			Args: []Arg{
				{Name: "method", Type: TypeString},
				{Name: "url", Type: TypeString},
				{Name: "body", Type: TypeString, Optional: true},
				{Name: "headers", Type: TypeStringMap, Optional: true},
			},
			Returns: []Ret{{Name: "status", Type: TypeInt}, {Name: "body", Type: TypeString}},
			Impl:    HTTPRequest,
		},
	},
}

var defaultHTTPClient = retry.NewHTTPClient(
	nil,
	retry.WithAttempts(3),
	retry.WithDelay(500*time.Millisecond),
)

// HTTPGet sends a GET request to url with optional headers and returns (status, body).
func HTTPGet(ctx context.Context, url string, headers map[string]string) (int, string, error) {
	return doRequest(ctx, http.MethodGet, url, "", headers)
}

// HTTPPost sends a POST of body to url with optional headers and returns (status, body).
func HTTPPost(ctx context.Context, url, body string, headers map[string]string) (int, string, error) {
	return doRequest(ctx, http.MethodPost, url, body, headers)
}

// HTTPRequest sends a request with the given method to url and returns (status, body).
func HTTPRequest(ctx context.Context, method, url, body string, headers map[string]string) (int, string, error) {
	return doRequest(ctx, method, url, body, headers)
}

func doRequest(ctx context.Context, method, url, body string, headers map[string]string) (int, string, error) {
	// Audit log every outbound request when the sandbox is active.
	// Not yet blocked; this gives operators visibility before enforcement lands.
	if p := sandbox.FromContext(ctx); p != nil {
		p.RecordConnect(ctx, method, url)
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return 0, "", fmt.Errorf("http.%s: %w", strings.ToLower(method), err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("http.%s %s: %w", strings.ToLower(method), url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", fmt.Errorf("http.%s %s: read body: %w", strings.ToLower(method), url, err)
	}
	return resp.StatusCode, string(raw), nil
}
